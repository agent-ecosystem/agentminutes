package driftprobe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/agent-ecosystem/agentminutes"
	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/parseutil"
	"github.com/agent-ecosystem/agentminutes/session"
	"github.com/agent-ecosystem/agentsummons"
)

// Runner drives one harness headlessly. The per-harness invocation
// knowledge (binaries, flags, prompt mechanics) lives in the agentsummons
// library — the invocation-side companion to this library's parsing —
// so DefaultRunners delegates invocation to it and transcript roots to
// the Locator registry. The struct itself is the seam that keeps the
// probe engine testable without real binaries.
type Runner struct {
	ID harness.ID

	// Locator supplies the harness's transcript-discovery rules: fresh
	// probe transcripts are found through it (the same discovery
	// production uses), never by a bare filename walk, so sidecar files
	// the locator excludes (notably Antigravity's trimmed
	// transcript.jsonl) cannot masquerade as probe transcripts.
	Locator harness.Locator

	// TranscriptRoot is the directory under which the harness stores
	// transcripts; fresh ones are located by modification time. Separate
	// from Locator.DefaultRoot so tests can point discovery at a fake
	// root.
	TranscriptRoot func() (string, error)

	// Version reports the installed harness version. A missing binary
	// must be reported with an error satisfying
	// errors.Is(err, exec.ErrNotFound).
	Version func(ctx context.Context) (string, error)

	// Invoke runs one headless invocation inside workdir and returns the
	// combined output. A nonzero harness exit or a timeout is an error;
	// partial output may accompany it.
	Invoke func(ctx context.Context, workdir string, inv Invocation) ([]byte, error)
}

// Invocation is one probe invocation's per-run options, mapped onto the
// harness command line by the Runner (DefaultRunners delegates the flag
// spellings to agentsummons).
type Invocation struct {
	Prompt string

	// AllowedTools restricts the run to the named tools, on harnesses
	// that support a restriction (Claude Code).
	AllowedTools []string

	// ExtraArgs are verbatim harness flags — the escape hatch for
	// anything AllowedTools doesn't model. A flag spelled here breaks in
	// this repo when the harness renames it; prefer typed fields.
	ExtraArgs []string
}

// DefaultRunners returns one runner per registered locator (alphabetical,
// like the registries), invoking through agentsummons.
func DefaultRunners() []Runner {
	locators := agentminutes.Locators()
	runners := make([]Runner, 0, len(locators))
	for _, l := range locators {
		id := agentsummons.ID(l.ID())
		runners = append(runners, Runner{
			ID:             l.ID(),
			Locator:        l,
			TranscriptRoot: l.DefaultRoot,
			Version: func(ctx context.Context) (string, error) {
				return agentsummons.Version(ctx, id)
			},
			Invoke: func(ctx context.Context, workdir string, inv Invocation) ([]byte, error) {
				res, err := agentsummons.Run(ctx, agentsummons.Request{
					Harness: id,
					Prompt:  inv.Prompt,
					Workdir: workdir,
					// Probes must run unattended; containment is the
					// disposable workdir, as before.
					AutoApprove:  true,
					AllowedTools: inv.AllowedTools,
					ExtraArgs:    inv.ExtraArgs,
				})
				if res == nil {
					return nil, err
				}
				out := append(res.Stdout, res.Stderr...)
				if err != nil {
					return out, err
				}
				if res.ExitCode != 0 {
					return out, fmt.Errorf("exit code %d", res.ExitCode)
				}
				return out, nil
			},
		})
	}
	return runners
}

// Probe is one headless task plus the shapes its transcript must contain.
// A probe whose shapes never appear is inconclusive, not drift: the model
// may simply not have used the tool, so it is retried once with the more
// insistent prompt before being reported.
type Probe struct {
	Name   string
	Prompt string
	Retry  string

	// Harnesses restricts the probe to the listed harnesses; empty means
	// all. For tool families that are dedicated-tool vocabulary on some
	// harnesses only (e.g. Claude Code's Grep/Glob search tools, where
	// other harnesses search via the shell, already covered by the shell
	// probe).
	Harnesses []harness.ID

	// Files seeds the disposable workdir before the probe's invocations
	// (slash-separated relative path to content), for probes that need
	// something to find. The workdir is shared across a harness's probes,
	// so seeded files remain visible to the probes that follow.
	Files map[string]string

	// AllowedTools restricts the probe's runs to the named tools, on
	// harnesses that support a restriction. Pair with Harnesses.
	AllowedTools []string

	// ExtraArgs are additional CLI flags for this probe's invocations.
	// Inherently harness-specific, so pair with Harnesses. This is a
	// deliberate escape hatch across the agentsummons boundary (which
	// otherwise owns harness flag spellings): a flag named here breaks in
	// this repo when the harness renames it. Prefer typed fields.
	ExtraArgs []string

	// Missing returns descriptions of expected shapes absent from the
	// parsed sessions (a probe run can write more than one transcript,
	// e.g. subagents; shapes may appear in any of them).
	Missing func(sessions []*session.Session) []string
}

// appliesTo reports whether the probe runs for the given harness.
func (p *Probe) appliesTo(id harness.ID) bool {
	return len(p.Harnesses) == 0 || slices.Contains(p.Harnesses, id)
}

// DefaultProbes exercises every schema shape the adapters map. Prompts are
// fixed and harmless; containment comes from the disposable workdir.
func DefaultProbes() []Probe {
	return []Probe{
		{
			Name:    "qa",
			Prompt:  "Reply with exactly one word: pong",
			Retry:   "Answer with plain text only, no tools: reply with exactly one word: pong",
			Missing: missingAssistantText,
		},
		{
			Name:    "shell",
			Prompt:  "Use your shell tool to run exactly this command: echo drift-probe-shell. Report its output.",
			Retry:   "You must actually execute a command. Run `echo drift-probe-shell` with your shell/command tool now and show me its output. Do not answer without running it.",
			Missing: missingToolKind(session.ToolKindExecute),
		},
		{
			Name:    "file",
			Prompt:  "Use your file-writing or patch tool to create a file named probe.txt in the current working directory containing exactly this text: drift probe. Do not use shell redirection or shell commands.",
			Retry:   "You must actually write a file with a dedicated file-creation/edit/patch tool (not the shell, not echo/cat redirection). Create probe.txt with content 'drift probe' now.",
			Missing: missingToolKind(session.ToolKindEdit),
		},
		{
			Name: "search",
			// Dedicated glob/grep search tools are Claude Code vocabulary
			// (their result envelopes drift independently of the shell);
			// the other harnesses search via shell, which the shell probe
			// already exercises. Headless 2.1.197 sessions do not register
			// Glob/Grep at all (not even via ToolSearch) unless the tools
			// are named in the tool restriction; the permission side is
			// already covered by the skip flag.
			Harnesses:    []harness.ID{harness.ClaudeCode},
			AllowedTools: []string{"Glob", "Grep"},
			Files: map[string]string{
				"notes/alpha.txt": "alpha note; the needle is drift-probe-needle\n",
				"notes/beta.txt":  "beta note with nothing to find\n",
			},
			Prompt:  `Call the Glob tool with pattern "notes/*.txt" to list the text files in this directory, then call the Grep tool to find which of those files contains the string drift-probe-needle. Do not use Bash or any shell command, do not read files, and do not spawn subagents. Reply with the matching file's name.`,
			Retry:   `You must invoke the actual Glob and Grep tools yourself — not Bash, not find, not shell grep, not a subagent. Step 1: call Glob with pattern "notes/*.txt". Step 2: call Grep with pattern "drift-probe-needle". Then reply with the matching file's name.`,
			Missing: missingNamedTools("Glob", "Grep"),
		},
		{
			Name:    "fetch",
			Prompt:  "Use your web tool to fetch https://example.com/ and tell me the exact text of the page's main heading. Fetch the live page; do not answer from memory.",
			Retry:   "You must actually retrieve the page. Fetch https://example.com/ with your web/URL tool now and quote its main heading. Do not answer from memory.",
			Missing: missingToolKind(session.ToolKindFetch),
		},
		{
			Name:    "multi",
			Prompt:  "Complete both tasks using your tools: (1) run the shell command `echo drift-probe-multi`; (2) create a file named multi.txt containing exactly: multi",
			Retry:   "Use your tools for both tasks now: run `echo drift-probe-multi` with your shell tool, and write multi.txt (content: multi) with your file tool.",
			Missing: missingToolCalls(2),
		},
	}
}

func missingAssistantText(sessions []*session.Session) []string {
	for _, s := range sessions {
		for i := range s.Events {
			ev := &s.Events[i]
			if ev.Kind != session.KindAssistantMessage {
				continue
			}
			for _, b := range ev.AssistantMessage.Content {
				if b.Kind == session.ContentText && strings.TrimSpace(b.Text) != "" {
					return nil
				}
			}
		}
	}
	return []string{"assistant_message with text content"}
}

func missingToolKind(kind session.ToolKind) func([]*session.Session) []string {
	return func(sessions []*session.Session) []string {
		var missing []string
		call, result := false, false
		for _, s := range sessions {
			for _, ti := range s.ToolInteractions() {
				if ti.Call == nil || ti.Call.ToolCall.Kind != kind {
					continue
				}
				call = true
				if len(ti.Results) > 0 {
					result = true
				}
			}
		}
		if !call {
			missing = append(missing, fmt.Sprintf("tool_call of kind %q", kind))
		} else if !result {
			missing = append(missing, fmt.Sprintf("tool_result for the %q call", kind))
		}
		return missing
	}
}

// missingNamedTools requires each named tool to be called and resulted.
// Name-exact (unlike the kind-based checks) because the point is getting
// each tool's own result envelope into the observed vocabulary; a kind
// count would pass vacuously on siblings of the same kind (ToolSearch is
// also kind "search").
func missingNamedTools(names ...string) func([]*session.Session) []string {
	return func(sessions []*session.Session) []string {
		called := map[string]bool{}
		resulted := map[string]bool{}
		for _, s := range sessions {
			for _, ti := range s.ToolInteractions() {
				if ti.Call == nil {
					continue
				}
				called[ti.Call.ToolCall.Name] = true
				if len(ti.Results) > 0 {
					resulted[ti.Call.ToolCall.Name] = true
				}
			}
		}
		var missing []string
		for _, name := range names {
			switch {
			case !called[name]:
				missing = append(missing, fmt.Sprintf("tool_call for %q", name))
			case !resulted[name]:
				missing = append(missing, fmt.Sprintf("tool_result for the %q call", name))
			}
		}
		return missing
	}
}

func missingToolCalls(n int) func([]*session.Session) []string {
	return func(sessions []*session.Session) []string {
		count := 0
		for _, s := range sessions {
			for i := range s.Events {
				if s.Events[i].Kind == session.KindToolCall {
					count++
				}
			}
		}
		if count < n {
			return []string{fmt.Sprintf("at least %d tool_calls (got %d)", n, count)}
		}
		return nil
	}
}

// DefaultTimeout is the per-invocation timeout used when ProbeOptions
// leaves Timeout zero (the CLI flag default references it too).
const DefaultTimeout = 5 * time.Minute

// mtimeSlack widens the probe's transcript-freshness window against
// filesystem mtime granularity: a coarse-grained filesystem can stamp a
// transcript slightly before the probe's recorded start.
const mtimeSlack = 2 * time.Second

// ProbeOptions configure a probe run.
type ProbeOptions struct {
	// Force probes even when the installed version equals LastValidated.
	Force bool

	// Keep retains copies of the fresh transcripts in a reported directory.
	Keep bool

	// Timeout bounds each harness invocation; zero means DefaultTimeout.
	Timeout time.Duration

	// MissingBinaryIsError makes an absent harness binary an execution
	// error instead of a skip (set when the harness was selected
	// explicitly rather than defaulted).
	MissingBinaryIsError bool
}

// RunProbes drives each runner through the version gate and the probe set,
// returning the worst category across harnesses.
func RunProbes(w io.Writer, runners []Runner, probes []Probe, opts ProbeOptions) Category {
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	worst := Clean
	for i := range runners {
		worst = maxCategory(worst, runHarness(w, &runners[i], probes, opts))
	}
	return worst
}

func runHarness(w io.Writer, r *Runner, probes []Probe, opts ProbeOptions) Category {
	say(w, "%s:\n", r.ID)
	installed, err := r.Version(context.Background())
	if errors.Is(err, exec.ErrNotFound) {
		if opts.MissingBinaryIsError {
			say(w, "  error: %v\n", err)
			return ExecError
		}
		say(w, "  skipped: harness not installed (%v)\n", err)
		return Clean
	}
	if err != nil {
		say(w, "  error: reading version: %v\n", err)
		return ExecError
	}
	validated := harness.LastValidated(r.ID)
	newer := harness.VersionNewer(installed, validated)
	switch {
	case opts.Force:
		say(w, "  probing %s (last validated %s; --force)\n", installed, validated)
	case newer:
		say(w, "  probing %s (newer than last validated %s)\n", installed, validated)
	case harness.VersionNewer(validated, installed):
		say(w, "  skipped: installed %s is older than last validated %s\n", installed, validated)
		return Clean
	default:
		say(w, "  up to date: installed %s matches last validated (use --force to probe anyway)\n", installed)
		return Clean
	}

	baseline, err := LoadBaseline(r.ID)
	if err != nil {
		say(w, "  error: %v\n", err)
		return ExecError
	}
	root, err := r.TranscriptRoot()
	if err != nil {
		say(w, "  error: %v\n", err)
		return ExecError
	}
	workdir, err := os.MkdirTemp("", "agentminutes-drift-")
	if err != nil {
		say(w, "  error: %v\n", err)
		return ExecError
	}
	keepDir := ""
	if opts.Keep {
		keepDir = filepath.Join(workdir, "transcripts")
	} else {
		defer func() { _ = os.RemoveAll(workdir) }()
	}

	builder, err := newVocabBuilder(r.ID)
	if err != nil {
		say(w, "  error: %v\n", err)
		return ExecError
	}
	cat := Clean
	// claimed dedupes transcript files across probes: the mtime window has
	// slack, so a probe finishing just before the next one starts would
	// otherwise be captured twice (cross-contaminating shape assertions).
	claimed := map[string]bool{}
	for i := range probes {
		if !probes[i].appliesTo(r.ID) {
			continue
		}
		cat = maxCategory(cat, runOneProbe(w, r, &probes[i], opts, root, workdir, keepDir, builder, claimed))
	}

	diff := DiffBaseline(baseline, builder.finalize(""))
	diff.write(w)
	if diff.HasDrift() {
		cat = maxCategory(cat, Drift)
	}
	switch cat {
	case Clean:
		say(w, "  clean: %s at %s matches the validated vocabulary; update harness.LastValidated to %s\n", r.ID, installed, installed)
	case Drift:
		say(w, "  to reconcile: regenerate the baseline (agentminutes drift baseline --harness %s ...), update harness.LastValidated, update plans/%s-format-inventory.md, extend fixtures for changed shapes\n", r.ID, r.ID)
	}
	if keepDir != "" {
		if _, err := os.Stat(keepDir); err == nil {
			say(w, "  kept transcripts under %s\n", keepDir)
		} else {
			say(w, "  no transcripts kept (nothing captured, or keeping failed; see above)\n")
		}
	}
	return cat
}

// runOneProbe runs a probe (retrying once if its shapes are missing),
// strict-parses the fresh transcripts, and feeds the vocabulary builder.
func runOneProbe(w io.Writer, r *Runner, p *Probe, opts ProbeOptions, root, workdir, keepDir string, builder *vocabBuilder, claimed map[string]bool) Category {
	if err := seedFiles(workdir, p.Files); err != nil {
		say(w, "  probe %s: error: seeding workdir: %v\n", p.Name, err)
		return ExecError
	}
	inv := Invocation{Prompt: p.Prompt, AllowedTools: p.AllowedTools, ExtraArgs: p.ExtraArgs}
	sessions, cat := invokeAndParse(w, r, p.Name, inv, opts, root, workdir, keepDir, builder, claimed)
	if cat == ExecError {
		return ExecError
	}
	if missing := p.Missing(sessions); len(missing) > 0 {
		say(w, "  probe %s: expected shapes missing (%s); retrying once\n", p.Name, strings.Join(missing, "; "))
		inv.Prompt = p.Retry
		retrySessions, retryCat := invokeAndParse(w, r, p.Name+"-retry", inv, opts, root, workdir, keepDir, builder, claimed)
		cat = maxCategory(cat, retryCat)
		if retryCat != ExecError {
			sessions = append(sessions, retrySessions...)
		}
		if missing := p.Missing(sessions); len(missing) > 0 {
			say(w, "  probe %s: inconclusive, still missing: %s\n", p.Name, strings.Join(missing, "; "))
			return maxCategory(cat, Inconclusive)
		}
	}
	say(w, "  probe %s: shapes exercised and parsed\n", p.Name)
	return cat
}

// seedFiles writes a probe's fixture files into the workdir.
func seedFiles(workdir string, files map[string]string) error {
	for rel, content := range files {
		path := filepath.Join(workdir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func invokeAndParse(w io.Writer, r *Runner, name string, inv Invocation, opts ProbeOptions, root, workdir, keepDir string, builder *vocabBuilder, claimed map[string]bool) ([]*session.Session, Category) {
	start := time.Now().Add(-mtimeSlack)
	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()
	out, err := r.Invoke(ctx, workdir, inv)
	if err != nil {
		say(w, "  probe %s: error: harness invocation failed: %v\n%s", name, err, indent(tail(out), "    "))
		return nil, ExecError
	}
	// The mtime window has slack, so it can re-capture a transcript from a
	// probe that finished moments earlier; each file belongs to the first
	// probe that claims it.
	var files []string
	for _, f := range newTranscripts(r.Locator, root, start) {
		if !claimed[f] {
			claimed[f] = true
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		say(w, "  probe %s: error: no new transcript under %s\n", name, root)
		return nil, ExecError
	}

	adapter, err := agentminutes.AdapterFor(r.ID)
	if err != nil {
		say(w, "  probe %s: error: %v\n", name, err)
		return nil, ExecError
	}
	cat := Clean
	var sessions []*session.Session
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			say(w, "  probe %s: error: %v\n", name, err)
			return nil, ExecError
		}
		// The mtime window also sweeps in transcripts that other sessions
		// on this machine happen to write during the probe (e.g. an
		// interactive session in another terminal). Those must feed
		// neither the shape assertions nor the vocabulary diff; the
		// recorded cwd tells them apart via the veto. Only judgeable
		// after a clean parse — a foreign transcript that fails strict
		// parsing still reports (loud beats silently ignoring a parse
		// failure).
		s, fileCat, vetoed := checkAccounting(adapter, data, func(s *session.Session) bool {
			if foreignCwd(s, workdir) {
				say(w, "  probe %s: ignoring %s: written by another session (cwd %s)\n", name, f, s.Meta.CWD)
				return true
			}
			return false
		}, func(format string, args ...any) {
			say(w, "  probe %s: %s on %s\n", name, fmt.Sprintf(format, args...), f)
		})
		if vetoed {
			continue
		}
		if keepDir != "" {
			keep := filepath.Join(keepDir, name+"-"+filepath.Base(f))
			if err := os.MkdirAll(keepDir, 0o755); err != nil {
				say(w, "  probe %s: could not keep %s: %v\n", name, f, err)
			} else if err := os.WriteFile(keep, data, 0o600); err != nil {
				say(w, "  probe %s: could not keep %s: %v\n", name, f, err)
			}
		}
		cat = maxCategory(cat, fileCat)
		if s != nil {
			sessions = append(sessions, s)
		}
		if err := builder.addTranscript(data); err != nil {
			say(w, "  probe %s: vocabulary extraction stopped on %s: %v\n", name, f, err)
			cat = maxCategory(cat, Drift)
		}
	}
	return sessions, cat
}

// checkAccounting runs the strict-parse and per-line-accounting checks
// shared by Scan and the probe engine, reporting each failure via report.
// The session is nil when parsing failed. veto, when non-nil, inspects a
// successfully parsed session before any accounting verdict; returning
// true discards the transcript entirely (the probe's foreign-session
// filter).
func checkAccounting(adapter harness.Adapter, data []byte, veto func(*session.Session) bool, report func(format string, args ...any)) (s *session.Session, cat Category, vetoed bool) {
	var skips []int
	s, err := harness.Parse(adapter, bytes.NewReader(data), harness.Options{
		OnSkip: func(line int, _ string) { skips = append(skips, line) },
	})
	if err != nil {
		report("drift: strict parse failed: %v", err)
		return nil, Drift, false
	}
	if veto != nil && veto(s) {
		return nil, Clean, true
	}
	if un := parseutil.UncoveredLines(data, s.Events, skips); len(un) > 0 {
		report("drift: %d lines covered by neither an event nor a skip (first: line %d)", len(un), un[0])
		return s, Drift, false
	}
	return s, Clean, false
}

// foreignCwd reports whether a parsed session records a working directory
// other than the probe workdir. Sessions recording no cwd cannot be judged
// and are kept — notably every Antigravity session, whose transcripts
// record no cwd at all, so a concurrent interactive Antigravity session
// always feeds the probe's checks.
func foreignCwd(s *session.Session, workdir string) bool {
	return s.Meta.CWD != "" && !sameDir(s.Meta.CWD, workdir)
}

// sameDir compares directories with symlinks resolved (macOS temp dirs are
// recorded as /private/var while MkdirTemp reports /var).
func sameDir(a, b string) bool {
	if ra, err := filepath.EvalSymlinks(a); err == nil {
		a = ra
	}
	if rb, err := filepath.EvalSymlinks(b); err == nil {
		b = rb
	}
	return a == b
}

// newTranscripts returns transcripts under root modified after since,
// oldest first, discovered through the harness's own Locator — the same
// discovery production uses. A fresh file the locator reports a ScanError
// for is still a candidate: an unidentifiable fresh transcript is exactly
// what the probe must look at (likely drift), while stale ScanErrors are
// other sessions' problems and are ignored. A missing root yields none
// (the no-new-transcript error is the loud signal).
func newTranscripts(l harness.Locator, root string, since time.Time) []string {
	type hit struct {
		path string
		mod  time.Time
	}
	var hits []hit
	add := func(path string) {
		if info, err := os.Stat(path); err == nil && info.ModTime().After(since) {
			hits = append(hits, hit{path, info.ModTime()})
		}
	}
	for ref, err := range l.Scan(root, harness.ScanOptions{Since: since}) {
		if err != nil {
			var se *harness.ScanError
			if errors.As(err, &se) && se.Path != "" {
				add(se.Path)
			}
			continue
		}
		add(ref.Path)
		for _, sub := range ref.SubagentPaths {
			add(sub)
		}
	}
	slices.SortStableFunc(hits, func(a, b hit) int { return a.mod.Compare(b.mod) })
	files := make([]string, len(hits))
	for i, h := range hits {
		files[i] = h.path
	}
	return files
}

func tail(out []byte) []byte {
	const max = 800
	out = bytes.TrimSpace(out)
	if len(out) > max {
		out = out[len(out)-max:]
	}
	return out
}

func indent(b []byte, prefix string) string {
	if len(b) == 0 {
		return ""
	}
	return prefix + strings.ReplaceAll(string(b), "\n", "\n"+prefix) + "\n"
}
