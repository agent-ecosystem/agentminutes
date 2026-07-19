package driftprobe

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/agent-ecosystem/agentminutes"
	"github.com/agent-ecosystem/agentminutes/harness"
)

// Category grades a drift-check outcome. Order is severity (worst wins
// when aggregating); the CLI exit code is a separate mapping.
type Category int

// Outcome categories.
const (
	// Clean means everything parsed and matched the baseline (or the
	// version gate skipped the work).
	Clean Category = iota

	// Inconclusive means a probe's expected shapes never appeared, so the
	// run proves nothing about them (the model may not have used the tool).
	Inconclusive

	// ExecError means the check itself could not run (missing binary,
	// timeout, transcript not found).
	ExecError

	// Drift means a parse failure or vocabulary drift was found.
	Drift
)

// ExitCode maps a category onto the drift command's documented exit codes:
// 0 clean, 1 drift, 2 inconclusive, 3 execution error.
func (c Category) ExitCode() int {
	switch c {
	case Drift:
		return 1
	case Inconclusive:
		return 2
	case ExecError:
		return 3
	default:
		return 0
	}
}

// Scan checks one existing transcript for drift without invoking anything:
// strict parse with line accounting, then a vocabulary diff against the
// embedded baseline. The harness is auto-detected.
func Scan(w io.Writer, path string, data []byte) Category {
	head := data
	if len(head) > harness.SniffSize {
		head = head[:harness.SniffSize]
	}
	adapter, conf := agentminutes.Detect(head)
	if conf == harness.NoMatch {
		say(w, "%s: could not detect the source harness\n", path)
		return ExecError
	}
	id := adapter.ID()
	baseline, err := LoadBaseline(id)
	if err != nil {
		say(w, "%s: %v\n", path, err)
		return ExecError
	}
	say(w, "%s: %s\n", path, id)

	s, cat, _ := checkAccounting(adapter, data, nil, func(format string, args ...any) {
		say(w, "  "+format+"\n", args...)
	})
	if s != nil {
		if v := s.Meta.HarnessVersion; v != "" && harness.VersionNewer(v, harness.LastValidated(id)) {
			say(w, "  note: transcript version %s is newer than last validated %s\n", v, harness.LastValidated(id))
		}
	}

	builder, err := newVocabBuilder(id)
	if err != nil {
		say(w, "  %v\n", err)
		return maxCategory(cat, ExecError)
	}
	if err := builder.addTranscript(data); err != nil {
		// Malformed JSON already surfaced as a parse failure above; the
		// vocabulary pass just cannot add anything.
		say(w, "  vocabulary extraction stopped: %v\n", err)
		return maxCategory(cat, Drift)
	}
	diff := DiffBaseline(baseline, builder.finalize(""))
	diff.write(w)
	if diff.HasDrift() {
		cat = Drift
	}
	if cat == Clean {
		say(w, "  clean: matches the %s baseline\n", id)
	}
	return cat
}

// Generate builds a baseline from transcript files and directories,
// skipping files that do not sniff as the harness. A directory the
// harness's locator recognizes as a transcript root contributes exactly
// the transcripts the locator discovers — so sidecar files the locator
// excludes (notably Antigravity's trimmed transcript.jsonl) can never
// enter a baseline; any other directory is walked flat for .jsonl files
// (collections outside the native layout, e.g. probe --keep output).
func Generate(w io.Writer, id harness.ID, version string, paths []string) (*Baseline, error) {
	adapter, err := agentminutes.AdapterFor(id)
	if err != nil {
		return nil, err
	}
	locator, err := agentminutes.LocatorFor(id)
	if err != nil {
		return nil, err
	}
	files, err := collectTranscripts(w, locator, paths)
	if err != nil {
		return nil, err
	}
	builder, err := newVocabBuilder(id)
	if err != nil {
		return nil, err
	}
	used := 0
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		head := data
		if len(head) > harness.SniffSize {
			head = head[:harness.SniffSize]
		}
		if adapter.Sniff(head) == harness.NoMatch {
			say(w, "skipping %s: does not sniff as %s\n", f, id)
			continue
		}
		if err := builder.addTranscript(data); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		used++
	}
	if used == 0 {
		return nil, fmt.Errorf("no %s transcripts among the given paths", id)
	}
	say(w, "baseline built from %d transcripts\n", used)
	return builder.finalize(version), nil
}

// WriteBaseline marshals a baseline the way the embedded files are stored.
func WriteBaseline(w io.Writer, b *Baseline) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(b)
}

// collectTranscripts expands baseline args into a transcript file list:
// plain files as given; directories through the locator when it discovers
// any transcripts there (native-layout roots, with sidecars correctly
// excluded), and by a flat .jsonl walk otherwise.
func collectTranscripts(w io.Writer, locator harness.Locator, paths []string) ([]string, error) {
	var files []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			files = append(files, p)
			continue
		}
		var located []string
		for ref, err := range locator.Scan(p, harness.ScanOptions{}) {
			if err != nil {
				continue // unreadable/unidentifiable files never enter a baseline
			}
			located = append(located, ref.Path)
			located = append(located, ref.SubagentPaths...)
		}
		if len(located) > 0 {
			say(w, "%s: %d transcripts via the %s locator\n", p, len(located), locator.ID())
			files = append(files, located...)
			continue
		}
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
				files = append(files, path)
			}
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

func maxCategory(a, b Category) Category {
	if a > b {
		return a
	}
	return b
}
