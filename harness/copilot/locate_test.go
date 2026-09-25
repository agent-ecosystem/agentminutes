package copilot

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/locatetest"
)

func writeFile(t *testing.T, path, content string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// scanRoot builds a synthetic ~/.copilot/session-state layout:
//
//	sess-a/events.jsonl
//	sess-a/workspace.yaml                 skip (sidecar)
//	sess-a/checkpoints/index.md           skip (sidecar)
//	sess-b/events.jsonl
//	sess-bad/events.jsonl                 malformed -> ScanError
//	sess-empty/workspace.yaml             skip (no transcript)
//	.session-operation-locks/sess-a.lock  skip (hidden bookkeeping)
//	notes.txt                             skip (not a directory)
func scanRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mt := func(s string) time.Time {
		ts, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return ts
	}
	writeFile(t, filepath.Join(root, "sess-a", "events.jsonl"),
		startLine("sess-a", "/tmp/exp", "2026-09-20T10:00:00.000Z"), mt("2026-09-20T11:00:00Z"))
	writeFile(t, filepath.Join(root, "sess-a", "workspace.yaml"), "id: sess-a\n", mt("2026-09-20T11:00:00Z"))
	writeFile(t, filepath.Join(root, "sess-a", "checkpoints", "index.md"), "# Checkpoint History\n", mt("2026-09-20T11:00:00Z"))
	writeFile(t, filepath.Join(root, "sess-b", "events.jsonl"),
		startLine("sess-b", "/tmp/exp2", "2026-09-24T10:00:00.000Z"), mt("2026-09-24T11:00:00Z"))
	writeFile(t, filepath.Join(root, "sess-bad", "events.jsonl"), "not json\n", mt("2026-09-24T12:00:00Z"))
	writeFile(t, filepath.Join(root, "sess-empty", "workspace.yaml"), "id: sess-empty\n", mt("2026-09-24T12:00:00Z"))
	writeFile(t, filepath.Join(root, ".session-operation-locks", "sess-a.lock"), "", mt("2026-09-20T11:00:00Z"))
	writeFile(t, filepath.Join(root, "notes.txt"), "x", mt("2026-09-20T11:00:00Z"))
	return root
}

func collect(t *testing.T, root string, opts harness.ScanOptions) (refs []harness.SessionRef, errs []error) {
	t.Helper()
	for ref, err := range (Adapter{}).Scan(root, opts) {
		if err != nil {
			errs = append(errs, err)
			continue
		}
		refs = append(refs, ref)
	}
	return refs, errs
}

func TestScan(t *testing.T) {
	root := scanRoot(t)
	skips := make(map[string]string)
	refs, errs := collect(t, root, harness.ScanOptions{
		OnSkip: func(path, reason string) { skips[path] = reason },
	})

	if len(refs) != 2 || refs[0].Meta.SessionID != "sess-a" || refs[1].Meta.SessionID != "sess-b" {
		t.Fatalf("refs = %+v, want sess-a then sess-b", refs)
	}
	if refs[0].Meta.CWD != "/tmp/exp" || refs[0].Meta.HarnessVersion != "1.0.88" || refs[0].Meta.Harness != "copilot" {
		t.Errorf("sess-a meta = %+v", refs[0].Meta)
	}
	if refs[0].StartedAt == nil || !refs[0].StartedAt.Equal(time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("sess-a started at %v", refs[0].StartedAt)
	}
	if !strings.HasSuffix(refs[0].Path, filepath.Join("sess-a", "events.jsonl")) || refs[0].SizeBytes == 0 {
		t.Errorf("sess-a ref = %+v", refs[0])
	}

	if len(errs) != 1 {
		t.Fatalf("errs = %v, want 1", errs)
	}
	var se *harness.ScanError
	if !errors.As(errs[0], &se) || !strings.Contains(se.Path, "sess-bad") || se.Harness != harness.Copilot {
		t.Errorf("error = %v, want ScanError for the malformed transcript", errs[0])
	}

	want := map[string]string{
		filepath.Join(root, "sess-a", "workspace.yaml"):                "session sidecar",
		filepath.Join(root, "sess-a", "checkpoints", "index.md"):       "session sidecar",
		filepath.Join(root, "sess-empty", "workspace.yaml"):            "session directory without transcript",
		filepath.Join(root, ".session-operation-locks", "sess-a.lock"): "not a session directory",
		filepath.Join(root, "notes.txt"):                               "not a session directory",
	}
	if len(skips) != len(want) {
		t.Errorf("skips = %v, want %v", skips, want)
	}
	for path, reason := range want {
		if skips[path] != reason {
			t.Errorf("skip[%s] = %q, want %q", path, skips[path], reason)
		}
	}
}

func TestScanWindow(t *testing.T) {
	root := scanRoot(t)
	since, _ := time.Parse(time.RFC3339, "2026-09-23T00:00:00Z")
	refs, errs := collect(t, root, harness.ScanOptions{Since: since})
	if len(refs) != 1 || refs[0].Meta.SessionID != "sess-b" {
		t.Errorf("refs = %+v, want only sess-b", refs)
	}
	if len(errs) != 1 {
		t.Errorf("errs = %v, want the malformed-transcript error", errs)
	}
	until, _ := time.Parse(time.RFC3339, "2026-09-21T00:00:00Z")
	refs, _ = collect(t, root, harness.ScanOptions{Until: until})
	if len(refs) != 1 || refs[0].Meta.SessionID != "sess-a" {
		t.Errorf("refs = %+v, want only sess-a", refs)
	}
}

func TestLocate(t *testing.T) {
	root := scanRoot(t)
	ref, err := (Adapter{}).Locate(root, "sess-b")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Meta.SessionID != "sess-b" || ref.Meta.CWD != "/tmp/exp2" {
		t.Errorf("ref = %+v", ref)
	}

	if _, err := (Adapter{}).Locate(root, "nope"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing session: err = %v, want ErrNotExist", err)
	}
	if _, err := (Adapter{}).Locate(root, "../sess-b"); err == nil {
		t.Error("path-escaping session id: want error")
	}

	// A directory whose name disagrees with the in-band session id.
	writeFile(t, filepath.Join(root, "sess-c", "events.jsonl"),
		startLine("sess-b", "/tmp/exp2", "2026-09-24T12:00:00.000Z"), time.Date(2026, 9, 24, 13, 0, 0, 0, time.UTC))
	if _, err := (Adapter{}).Locate(root, "sess-c"); err == nil || !strings.Contains(err.Error(), "records session id") {
		t.Errorf("mismatch: err = %v", err)
	}

	// A head without session.start falls back to the directory name.
	writeFile(t, filepath.Join(root, "sess-d", "events.jsonl"),
		`{"type":"assistant.turn_start","data":{"turnId":"0"},"id":"e-9","timestamp":"2026-09-24T12:00:00.000Z","parentId":"e-8"}`+"\n",
		time.Date(2026, 9, 24, 13, 0, 0, 0, time.UTC))
	ref, err = (Adapter{}).Locate(root, "sess-d")
	if err != nil || ref.Meta.SessionID != "sess-d" {
		t.Errorf("layout fallback: ref = %+v, err = %v", ref, err)
	}
}

func TestDefaultRootHonorsCopilotHome(t *testing.T) {
	t.Setenv("COPILOT_HOME", filepath.Join("x", "copilot-home"))
	root, err := (Adapter{}).DefaultRoot()
	if err != nil || root != filepath.Join("x", "copilot-home", "session-state") {
		t.Errorf("DefaultRoot() = %q, %v", root, err)
	}
}

func TestLocalScan(t *testing.T) {
	root := os.Getenv("AGENTMINUTES_LOCAL_COPILOT_TRANSCRIPTS")
	if root == "" {
		t.Skip("set AGENTMINUTES_LOCAL_COPILOT_TRANSCRIPTS to run against real transcripts")
	}
	locatetest.Invariant(t, Adapter{}, root)
}

// TestLocalTask pins the subagent join on the real corpus: every subagent
// transcript discovery finds is gathered by exactly one task.
func TestLocalTask(t *testing.T) {
	root := os.Getenv("AGENTMINUTES_LOCAL_COPILOT_TRANSCRIPTS")
	if root == "" {
		t.Skip("set AGENTMINUTES_LOCAL_COPILOT_TRANSCRIPTS to run against real transcripts")
	}
	locatetest.TaskInvariant(t, Adapter{}, root)
}
