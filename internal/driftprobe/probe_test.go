package driftprobe

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/harness/claudecode"
	"github.com/agent-ecosystem/agentminutes/session"
)

// ccDriftRecord is a record type no claude-code baseline knows, shared by
// the drift tests so they exercise the same unknown-vocabulary shape.
const ccDriftRecord = `{"type":"hologram","uuid":"h-1"}`

// ccAnswer is a minimal claude-code transcript with an assistant text
// reply, staying inside the embedded baseline's vocabulary.
const ccAnswer = ccUser + "\n" +
	`{"parentUuid":"u-1","isSidechain":false,"type":"assistant","message":{"id":"m-1","role":"assistant","model":"x","content":[{"type":"text","text":"pong"}]},"uuid":"a-1","timestamp":"2026-07-19T10:00:01.000Z","sessionId":"s-1","version":"2.1.197"}` + "\n"

// fakeRunner builds a Runner whose Invoke runs a shell script that drops
// the canned transcripts into root (in the claude-code project layout the
// Locator discovers), imitating a harness writing fresh session logs;
// Version reports the given version without any binary, mirroring how
// DefaultRunners keeps the two concerns separate. The __WORKDIR__
// placeholder in a transcript is replaced with the probe workdir, so
// canned sessions read as written by the probe rather than as foreign. The
// script honors FAKE_REQUIRE (a workdir-relative file that must exist, for
// the seeding test).
func fakeRunner(t *testing.T, version string, transcripts ...string) (Runner, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake harness binary is a shell script")
	}
	root := t.TempDir()
	script := filepath.Join(t.TempDir(), "fake-claude")
	var body strings.Builder
	body.WriteString("#!/bin/sh\n")
	body.WriteString("if [ -n \"$FAKE_REQUIRE\" ] && [ ! -f \"$FAKE_REQUIRE\" ]; then echo \"missing seed $FAKE_REQUIRE\" >&2; exit 1; fi\n")
	body.WriteString("mkdir -p \"$FAKE_ROOT/-tmp-proj\"\n")
	for i, transcript := range transcripts {
		fmt.Fprintf(&body, `sed "s|__WORKDIR__|$FAKE_CWD|g" > "$FAKE_ROOT/-tmp-proj/session-$$-%d.jsonl" <<'TRANSCRIPT'
%sTRANSCRIPT
`, i, transcript)
	}
	if err := os.WriteFile(script, []byte(body.String()), 0o700); err != nil {
		t.Fatal(err)
	}
	return Runner{
		ID:             harness.ClaudeCode,
		Locator:        claudecode.Adapter{},
		TranscriptRoot: func() (string, error) { return root, nil },
		Version:        func(context.Context) (string, error) { return version, nil },
		Invoke: func(ctx context.Context, workdir string, inv Invocation) ([]byte, error) {
			cmd := exec.CommandContext(ctx, script, append(inv.ExtraArgs, inv.Prompt)...)
			cmd.Dir = workdir
			cmd.Env = append(os.Environ(), "FAKE_ROOT="+root, "FAKE_CWD="+workdir)
			return cmd.CombinedOutput()
		},
	}, root
}

func qaProbe() []Probe {
	return []Probe{{Name: "qa", Prompt: "p", Retry: "r", Missing: missingAssistantText}}
}

func TestRunProbesClean(t *testing.T) {
	r, _ := fakeRunner(t, "99.0.0", ccAnswer)
	var buf bytes.Buffer
	cat := RunProbes(&buf, []Runner{r}, qaProbe(), ProbeOptions{Timeout: time.Minute})
	if cat != Clean {
		t.Fatalf("category %v, want clean; report:\n%s", cat, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "probing 99.0.0") || !strings.Contains(out, "probe qa: shapes exercised") {
		t.Errorf("report:\n%s", out)
	}
}

func TestRunProbesVersionGate(t *testing.T) {
	r, _ := fakeRunner(t, harness.LastValidated(harness.ClaudeCode), ccAnswer)
	var buf bytes.Buffer
	if cat := RunProbes(&buf, []Runner{r}, qaProbe(), ProbeOptions{Timeout: time.Minute}); cat != Clean {
		t.Fatalf("category %v, want clean skip", cat)
	}
	if !strings.Contains(buf.String(), "up to date") {
		t.Errorf("report:\n%s", buf.String())
	}

	// --force probes anyway.
	buf.Reset()
	if cat := RunProbes(&buf, []Runner{r}, qaProbe(), ProbeOptions{Force: true, Timeout: time.Minute}); cat != Clean {
		t.Fatalf("forced category %v; report:\n%s", cat, buf.String())
	}
	if !strings.Contains(buf.String(), "--force") || !strings.Contains(buf.String(), "probe qa") {
		t.Errorf("forced report:\n%s", buf.String())
	}
}

func TestRunProbesInconclusive(t *testing.T) {
	// The canned transcript never contains an execute tool call, so the
	// probe retries once and reports inconclusive, not drift.
	r, _ := fakeRunner(t, "99.0.0", ccAnswer)
	probes := []Probe{{Name: "shell", Prompt: "p", Retry: "r", Missing: missingToolKind(session.ToolKindExecute)}}
	var buf bytes.Buffer
	if cat := RunProbes(&buf, []Runner{r}, probes, ProbeOptions{Timeout: time.Minute}); cat != Inconclusive {
		t.Fatalf("category %v, want inconclusive; report:\n%s", cat, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "retrying once") || !strings.Contains(out, "inconclusive") {
		t.Errorf("report:\n%s", out)
	}
}

func TestRunProbesDrift(t *testing.T) {
	drifted := ccAnswer + ccDriftRecord + "\n"
	r, _ := fakeRunner(t, "99.0.0", drifted)
	var buf bytes.Buffer
	if cat := RunProbes(&buf, []Runner{r}, qaProbe(), ProbeOptions{Timeout: time.Minute}); cat != Drift {
		t.Fatalf("category %v, want drift; report:\n%s", cat, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "strict parse failed") || !strings.Contains(out, "to reconcile") {
		t.Errorf("report:\n%s", out)
	}
}

func TestRunProbesMissingBinary(t *testing.T) {
	r := Runner{ID: harness.ClaudeCode, Version: func(context.Context) (string, error) {
		return "", fmt.Errorf(`harness "claude-code" binary "claude" not found in PATH: %w`, exec.ErrNotFound)
	}}
	var buf bytes.Buffer
	if cat := RunProbes(&buf, []Runner{r}, qaProbe(), ProbeOptions{Timeout: time.Minute}); cat != Clean {
		t.Errorf("default missing binary: category %v, want clean skip", cat)
	}
	buf.Reset()
	opts := ProbeOptions{Timeout: time.Minute, MissingBinaryIsError: true}
	if cat := RunProbes(&buf, []Runner{r}, qaProbe(), opts); cat != ExecError {
		t.Errorf("explicit missing binary: category %v, want exec error", cat)
	}
}

// TestProbeTranscriptsNotDoubleClaimed pins the mtime-window dedupe: the
// fake harness runs fast enough that every probe's window (2s slack) also
// covers the previous probe's transcript, so without per-run claiming the
// second probe would see two sessions.
func TestProbeTranscriptsNotDoubleClaimed(t *testing.T) {
	r, _ := fakeRunner(t, "99.0.0", ccAnswer)
	var counts []int
	countProbe := func(name string) Probe {
		return Probe{Name: name, Prompt: "p", Retry: "r", Missing: func(sessions []*session.Session) []string {
			counts = append(counts, len(sessions))
			return nil
		}}
	}
	var buf bytes.Buffer
	cat := RunProbes(&buf, []Runner{r}, []Probe{countProbe("one"), countProbe("two")}, ProbeOptions{Timeout: time.Minute})
	if cat != Clean {
		t.Fatalf("category %v; report:\n%s", cat, buf.String())
	}
	for i, n := range counts {
		if n != 1 {
			t.Errorf("probe %d saw %d sessions, want exactly its own", i+1, n)
		}
	}
}

// TestProbeIgnoresForeignTranscripts pins the cwd filter: a transcript
// another session writes during the probe's mtime window (recognizable by
// its cwd) must not feed the vocabulary diff, even when it carries keys the
// baseline has never seen.
func TestProbeIgnoresForeignTranscripts(t *testing.T) {
	foreign := strings.ReplaceAll(ccAnswer, `"cwd":"__WORKDIR__"`, `"cwd":"/somewhere/else","flux":9`)
	if foreign == ccAnswer {
		t.Fatal("placeholder substitution failed; fixture changed?")
	}
	r, _ := fakeRunner(t, "99.0.0", ccAnswer, foreign)
	var buf bytes.Buffer
	if cat := RunProbes(&buf, []Runner{r}, qaProbe(), ProbeOptions{Timeout: time.Minute}); cat != Clean {
		t.Fatalf("category %v, want clean; report:\n%s", cat, buf.String())
	}
	if !strings.Contains(buf.String(), "written by another session") {
		t.Errorf("report does not mention the ignored transcript:\n%s", buf.String())
	}
}

// TestProbeHarnessFilter pins that a probe scoped to another harness is not
// run: its Missing check never fires, and the report never mentions it.
func TestProbeHarnessFilter(t *testing.T) {
	r, _ := fakeRunner(t, "99.0.0", ccAnswer)
	probes := append(qaProbe(), Probe{
		Name:      "codex-only",
		Harnesses: []harness.ID{harness.Codex},
		Prompt:    "p",
		Retry:     "r",
		Missing: func([]*session.Session) []string {
			t.Error("codex-only probe ran against claude-code")
			return nil
		},
	})
	var buf bytes.Buffer
	if cat := RunProbes(&buf, []Runner{r}, probes, ProbeOptions{Timeout: time.Minute}); cat != Clean {
		t.Fatalf("category %v; report:\n%s", cat, buf.String())
	}
	if strings.Contains(buf.String(), "codex-only") {
		t.Errorf("report mentions the filtered probe:\n%s", buf.String())
	}
}

// TestProbeSeedsWorkdirFiles pins that Probe.Files land in the workdir
// before the harness runs: the fake harness refuses to write a transcript
// unless the seeded file is present (workdir-relative; fakeRunner's Invoke
// runs the script with the workdir as cwd), so a Clean run proves the
// seeding.
func TestProbeSeedsWorkdirFiles(t *testing.T) {
	r, _ := fakeRunner(t, "99.0.0", ccAnswer)
	t.Setenv("FAKE_REQUIRE", "notes/alpha.txt")
	probes := qaProbe()
	probes[0].Files = map[string]string{"notes/alpha.txt": "drift-probe-needle\n"}
	var buf bytes.Buffer
	if cat := RunProbes(&buf, []Runner{r}, probes, ProbeOptions{Timeout: time.Minute}); cat != Clean {
		t.Fatalf("category %v; report:\n%s", cat, buf.String())
	}
}

// TestDefaultRunnersAlphabetical keeps the runner table in step with the
// registry and the alphabetical-lists rule.
func TestDefaultRunnersAlphabetical(t *testing.T) {
	runners := DefaultRunners()
	want := []harness.ID{harness.Antigravity, harness.ClaudeCode, harness.Codex, harness.Copilot}
	if len(runners) != len(want) {
		t.Fatalf("got %d runners, want %d", len(runners), len(want))
	}
	for i, id := range want {
		if runners[i].ID != id {
			t.Errorf("runner %d = %q, want %q", i, runners[i].ID, id)
		}
	}
}

// TestCheckTaskJoin pins the subagent probe's file-level check on a
// synthetic Claude Code task: a parent with a subagent file passes (the
// join finds the subagent and the totals sum), and a parent whose
// subagent file is missing reports the join as missing.
func TestCheckTaskJoin(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "-tmp-proj")
	line := func(sessionID, uuid, parent, ts string, sidechain bool, agentID, kind, message string) string {
		side := "false"
		if sidechain {
			side = "true"
		}
		agent := ""
		if agentID != "" {
			agent = `"agentId":"` + agentID + `",`
		}
		return `{"parentUuid":` + parent + `,"isSidechain":` + side + `,` + agent + `"type":"` + kind + `","message":` + message + `,"uuid":"` + uuid + `","timestamp":"` + ts + `","userType":"external","entrypoint":"cli","cwd":"/tmp/x","sessionId":"` + sessionID + `","version":"2.1.274"}` + "\n"
	}
	asst := `{"id":"m","model":"x","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":3,"output_tokens":4,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}`
	write := func(rel, content string) string {
		p := filepath.Join(proj, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	parent := write("p-1.jsonl", line("p-1", "u1", "null", "2026-09-25T10:00:00.000Z", false, "", "user", `{"role":"user","content":"go"}`)+line("p-1", "a1", `"u1"`, "2026-09-25T10:00:01.000Z", false, "", "assistant", asst))
	write("p-1/subagents/agent-c1.jsonl", line("p-1", "u2", "null", "2026-09-25T10:00:00.500Z", true, "c1", "user", `{"role":"user","content":"sub"}`)+line("p-1", "a2", `"u2"`, "2026-09-25T10:00:00.900Z", true, "c1", "assistant", asst))
	lonely := write("p-2.jsonl", line("p-2", "u3", "null", "2026-09-25T10:00:00.000Z", false, "", "user", `{"role":"user","content":"go"}`))

	ctx := CheckContext{Harness: harness.ClaudeCode, Locator: claudecode.Adapter{}, Root: root, Files: []string{parent}}
	if f := checkTaskJoin(ctx); len(f) != 0 {
		t.Errorf("joined task reported findings %v", f)
	}
	ctx.Files = []string{lonely}
	if f := checkTaskJoin(ctx); len(f) != 1 || !strings.Contains(f[0], "layout join") {
		t.Errorf("parent without subagent: findings = %v", f)
	}
}
