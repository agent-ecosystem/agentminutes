package codex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/locatetest"
)

// metaLine renders a minimal session_meta record.
func metaLine(sessionID, cwd, ts string) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"session_id":%q,"id":%q,"timestamp":%q,"cwd":%q,"originator":"codex_exec","cli_version":"0.144.1","source":"exec","git":{"branch":"main"}}}`,
		ts, sessionID, sessionID, ts, cwd) + "\n"
}

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

// scanRoot builds a synthetic ~/.codex/sessions layout:
//
//	2026/07/20/rollout-2026-07-20T10-00-00-c-1.jsonl
//	2026/07/20/notes.txt                              skip (and pruning probe)
//	2026/07/24/rollout-2026-07-24T10-00-00-c-2.jsonl
//	2026/07/24/rollout-2026-07-24T11-00-00-c-bad.jsonl  malformed -> ScanError
//	README.md                                         skip
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
	writeFile(t, filepath.Join(root, "2026", "07", "20", "rollout-2026-07-20T10-00-00-c-1.jsonl"),
		metaLine("c-1", "/tmp/exp", "2026-07-20T10:00:00.000Z"), mt("2026-07-20T11:00:00Z"))
	writeFile(t, filepath.Join(root, "2026", "07", "20", "notes.txt"), "x", mt("2026-07-20T11:00:00Z"))
	writeFile(t, filepath.Join(root, "2026", "07", "24", "rollout-2026-07-24T10-00-00-c-2.jsonl"),
		metaLine("c-2", "/tmp/exp2", "2026-07-24T10:00:00.000Z"), mt("2026-07-24T11:00:00Z"))
	writeFile(t, filepath.Join(root, "2026", "07", "24", "rollout-2026-07-24T11-00-00-c-bad.jsonl"),
		"not json\n", mt("2026-07-24T12:00:00Z"))
	writeFile(t, filepath.Join(root, "README.md"), "x", mt("2026-07-20T11:00:00Z"))
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

	if len(refs) != 2 || refs[0].Meta.SessionID != "c-1" || refs[1].Meta.SessionID != "c-2" {
		t.Fatalf("refs = %+v, want c-1 then c-2", refs)
	}
	if refs[0].Meta.CWD != "/tmp/exp" || refs[0].Meta.HarnessVersion != "0.144.1" {
		t.Errorf("c-1 meta = %+v", refs[0].Meta)
	}
	if refs[0].StartedAt == nil || !refs[0].StartedAt.Equal(time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("c-1 started at %v", refs[0].StartedAt)
	}

	if len(errs) != 1 {
		t.Fatalf("errs = %v, want 1", errs)
	}
	var se *harness.ScanError
	if !errors.As(errs[0], &se) || !strings.Contains(se.Path, "c-bad") || se.Harness != harness.Codex {
		t.Errorf("error = %v, want ScanError for the malformed rollout", errs[0])
	}

	if len(skips) != 2 {
		t.Errorf("skips = %v, want notes.txt and README.md", skips)
	}
}

// TestScanPrunesDateDirectories proves whole day directories outside the
// window are never walked: the skip probe inside the pruned day goes
// unreported.
func TestScanPrunesDateDirectories(t *testing.T) {
	root := scanRoot(t)
	since, _ := time.Parse(time.RFC3339, "2026-07-23T00:00:00Z")
	skips := make(map[string]string)
	refs, errs := collect(t, root, harness.ScanOptions{
		Since:  since,
		OnSkip: func(path, reason string) { skips[path] = reason },
	})
	if len(refs) != 1 || refs[0].Meta.SessionID != "c-2" {
		t.Errorf("refs = %+v, want only c-2", refs)
	}
	if len(errs) != 1 {
		t.Errorf("errs = %v, want the malformed-rollout error", errs)
	}
	if probe := filepath.Join(root, "2026", "07", "20", "notes.txt"); skips[probe] != "" {
		t.Errorf("pruned day was walked: skip reported for %s", probe)
	}
}

func TestLocate(t *testing.T) {
	root := scanRoot(t)
	ref, err := (Adapter{}).Locate(root, "c-2")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Meta.SessionID != "c-2" || ref.Meta.CWD != "/tmp/exp2" {
		t.Errorf("ref = %+v", ref)
	}

	if _, err := (Adapter{}).Locate(root, "nope"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing session: err = %v, want ErrNotExist", err)
	}

	// A rollout whose filename disagrees with its in-band session id.
	writeFile(t, filepath.Join(root, "2026", "07", "24", "rollout-2026-07-24T12-00-00-c-9.jsonl"),
		metaLine("c-2", "/tmp/exp2", "2026-07-24T12:00:00.000Z"), time.Date(2026, 7, 24, 13, 0, 0, 0, time.UTC))
	if _, err := (Adapter{}).Locate(root, "c-9"); err == nil || !strings.Contains(err.Error(), "records session id") {
		t.Errorf("mismatch: err = %v", err)
	}
}

func TestLocalScan(t *testing.T) {
	root := os.Getenv("AGENTMINUTES_LOCAL_CODEX_TRANSCRIPTS")
	if root == "" {
		t.Skip("set AGENTMINUTES_LOCAL_CODEX_TRANSCRIPTS to run against real transcripts")
	}
	locatetest.Invariant(t, Adapter{}, root)
}
