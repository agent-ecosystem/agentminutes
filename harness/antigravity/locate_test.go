package antigravity

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

const userInputLine = `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-07-20T10:00:00Z","content":"<USER_REQUEST>\nhello\n</USER_REQUEST>"}` + "\n"

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

// scanRoot builds a synthetic brain layout:
//
//	conv-1/.system_generated/logs/transcript_full.jsonl
//	conv-1/.system_generated/steps/0/content.md          sidecar
//	conv-2/notes.txt                                     no transcript
//	conv-3/.system_generated/logs/transcript_full.jsonl  malformed -> ScanError
//	stray.txt                                            skip
func scanRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mt := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC)
	writeFile(t, filepath.Join(root, "conv-1", ".system_generated", "logs", "transcript_full.jsonl"), userInputLine, mt)
	writeFile(t, filepath.Join(root, "conv-1", ".system_generated", "steps", "0", "content.md"), "x", mt)
	writeFile(t, filepath.Join(root, "conv-2", "notes.txt"), "x", mt)
	writeFile(t, filepath.Join(root, "conv-3", ".system_generated", "logs", "transcript_full.jsonl"), "not json\n", mt)
	writeFile(t, filepath.Join(root, "stray.txt"), "x", mt)
	return root
}

func TestScan(t *testing.T) {
	root := scanRoot(t)
	skips := make(map[string]string)
	var refs []harness.SessionRef
	var errs []error
	for ref, err := range (Adapter{}).Scan(root, harness.ScanOptions{
		OnSkip: func(path, reason string) { skips[path] = reason },
	}) {
		if err != nil {
			errs = append(errs, err)
			continue
		}
		refs = append(refs, ref)
	}

	if len(refs) != 1 {
		t.Fatalf("refs = %+v, want 1", refs)
	}
	ref := refs[0]
	if ref.Meta.SessionID != "conv-1" || ref.Meta.Harness != "antigravity" {
		t.Errorf("meta = %+v, want dirname-derived session id", ref.Meta)
	}
	if ref.StartedAt == nil || !ref.StartedAt.Equal(time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("started at %v", ref.StartedAt)
	}

	if len(errs) != 1 {
		t.Fatalf("errs = %v, want 1", errs)
	}
	var se *harness.ScanError
	if !errors.As(errs[0], &se) || !strings.Contains(se.Path, "conv-3") || se.Harness != harness.Antigravity {
		t.Errorf("error = %v, want ScanError for conv-3", errs[0])
	}

	wantSkips := map[string]string{
		filepath.Join(root, "conv-1", ".system_generated", "steps", "0", "content.md"): "conversation sidecar",
		filepath.Join(root, "conv-2", "notes.txt"):                                     "conversation directory without transcript",
		filepath.Join(root, "stray.txt"):                                               "not a conversation directory",
	}
	if len(skips) != len(wantSkips) {
		t.Errorf("skips = %v, want %v", skips, wantSkips)
	}
	for path, reason := range wantSkips {
		if skips[path] != reason {
			t.Errorf("skip[%s] = %q, want %q", path, skips[path], reason)
		}
	}
}

func TestScanWindow(t *testing.T) {
	root := scanRoot(t)
	until, _ := time.Parse(time.RFC3339, "2026-07-19T00:00:00Z")
	var refs []harness.SessionRef
	for ref, err := range (Adapter{}).Scan(root, harness.ScanOptions{Until: until}) {
		if err != nil {
			continue
		}
		refs = append(refs, ref)
	}
	if len(refs) != 0 {
		t.Errorf("refs = %+v, want none before the window", refs)
	}
}

func TestLocate(t *testing.T) {
	root := scanRoot(t)
	ref, err := (Adapter{}).Locate(root, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Meta.SessionID != "conv-1" || !strings.HasSuffix(ref.Path, "transcript_full.jsonl") {
		t.Errorf("ref = %+v", ref)
	}

	if _, err := (Adapter{}).Locate(root, "conv-9"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing conversation: err = %v, want ErrNotExist", err)
	}
	if _, err := (Adapter{}).Locate(root, "../evil"); err == nil {
		t.Error("path-escaping session id not rejected")
	}
}

func TestLocalScan(t *testing.T) {
	root := os.Getenv("AGENTMINUTES_LOCAL_ANTIGRAVITY_TRANSCRIPTS")
	if root == "" {
		t.Skip("set AGENTMINUTES_LOCAL_ANTIGRAVITY_TRANSCRIPTS to run against real transcripts")
	}
	locatetest.Invariant(t, Adapter{}, root)
}
