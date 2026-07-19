package claudecode

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

// ccLine renders one minimal conversation record.
func ccLine(sessionID, cwd, ts string, sidechain bool, agentID string) string {
	agent := ""
	if agentID != "" {
		agent = fmt.Sprintf(`"agentId":%q,`, agentID)
	}
	return fmt.Sprintf(`{"parentUuid":null,"isSidechain":%v,%s"type":"user","message":{"role":"user","content":"hello"},"uuid":"u-1","timestamp":%q,"userType":"external","entrypoint":"cli","cwd":%q,"sessionId":%q,"version":"2.1.197","gitBranch":"main"}`,
		sidechain, agent, ts, cwd, sessionID) + "\n"
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

// scanRoot builds a synthetic ~/.claude/projects layout:
//
//	-tmp-exp/bad.jsonl                          malformed -> ScanError
//	-tmp-exp/notes.txt                          skip
//	-tmp-exp/s-1.jsonl                          session, cwd /tmp/exp
//	-tmp-exp/s-1/subagents/agent-a1.jsonl       subagent of s-1
//	-tmp-exp/s-1/checkpoint.bin                 session directory sidecar
//	-tmp-exp2/s-2.jsonl                         session, cwd /tmp/exp2
//	-tmp-orphan/s-3/subagents/agent-o1.jsonl    orphan subagent
//	stray.txt                                   skip
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
	writeFile(t, filepath.Join(root, "-tmp-exp", "s-1.jsonl"),
		ccLine("s-1", "/tmp/exp", "2026-07-20T10:00:00.000Z", false, ""), mt("2026-07-20T11:00:00Z"))
	writeFile(t, filepath.Join(root, "-tmp-exp", "s-1", "subagents", "agent-a1.jsonl"),
		ccLine("s-1", "/tmp/exp", "2026-07-20T10:01:00.000Z", true, "a1"), mt("2026-07-20T11:00:00Z"))
	writeFile(t, filepath.Join(root, "-tmp-exp", "s-1", "checkpoint.bin"), "x", mt("2026-07-20T11:00:00Z"))
	writeFile(t, filepath.Join(root, "-tmp-exp", "bad.jsonl"), "not json\n", mt("2026-07-20T11:00:00Z"))
	writeFile(t, filepath.Join(root, "-tmp-exp", "notes.txt"), "x", mt("2026-07-20T11:00:00Z"))
	writeFile(t, filepath.Join(root, "-tmp-exp2", "s-2.jsonl"),
		ccLine("s-2", "/tmp/exp2", "2026-07-24T10:00:00.000Z", false, ""), mt("2026-07-24T11:00:00Z"))
	writeFile(t, filepath.Join(root, "-tmp-orphan", "s-3", "subagents", "agent-o1.jsonl"),
		ccLine("s-3", "/tmp/orphan", "2026-07-20T10:00:00.000Z", true, "o1"), mt("2026-07-20T11:00:00Z"))
	writeFile(t, filepath.Join(root, "stray.txt"), "x", mt("2026-07-20T11:00:00Z"))
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

	if len(errs) != 1 {
		t.Fatalf("got %d errors %v, want 1", len(errs), errs)
	}
	var se *harness.ScanError
	if !errors.As(errs[0], &se) || !strings.HasSuffix(se.Path, "bad.jsonl") || se.Harness != harness.ClaudeCode {
		t.Errorf("error = %v, want ScanError for bad.jsonl", errs[0])
	}

	if len(refs) != 3 {
		t.Fatalf("got %d refs %+v, want 3", len(refs), refs)
	}
	s1, s2, orphan := refs[0], refs[1], refs[2]
	if s1.Meta.SessionID != "s-1" || s1.Meta.CWD != "/tmp/exp" || s1.Meta.Harness != "claude-code" {
		t.Errorf("s-1 meta = %+v", s1.Meta)
	}
	if len(s1.SubagentPaths) != 1 || !strings.HasSuffix(s1.SubagentPaths[0], "agent-a1.jsonl") {
		t.Errorf("s-1 subagents = %v", s1.SubagentPaths)
	}
	if s1.StartedAt == nil || !s1.StartedAt.Equal(time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("s-1 started at %v", s1.StartedAt)
	}
	if s1.SizeBytes == 0 || s1.ModTime.IsZero() {
		t.Errorf("s-1 stat fields missing: %+v", s1)
	}
	if s2.Meta.SessionID != "s-2" || s2.Meta.CWD != "/tmp/exp2" {
		t.Errorf("s-2 meta = %+v", s2.Meta)
	}
	if !orphan.Meta.IsSubagent || orphan.Meta.SubagentID != "o1" || orphan.Meta.SessionID != "s-3" {
		t.Errorf("orphan meta = %+v", orphan.Meta)
	}
	if !strings.HasSuffix(orphan.Path, "agent-o1.jsonl") || len(orphan.SubagentPaths) != 0 {
		t.Errorf("orphan ref = %+v", orphan)
	}

	wantSkips := map[string]string{
		filepath.Join(root, "-tmp-exp", "notes.txt"):             "not a session transcript",
		filepath.Join(root, "-tmp-exp", "s-1", "checkpoint.bin"): "session directory sidecar",
		filepath.Join(root, "stray.txt"):                         "not a project directory",
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
	since, _ := time.Parse(time.RFC3339, "2026-07-22T00:00:00Z")
	refs, errs := collect(t, root, harness.ScanOptions{Since: since})
	if len(refs) != 1 || refs[0].Meta.SessionID != "s-2" {
		t.Errorf("refs = %+v, want only s-2", refs)
	}
	// The malformed file still errors: its window is unknowable.
	if len(errs) != 1 {
		t.Errorf("errs = %v, want the bad.jsonl error", errs)
	}
}

func TestLocate(t *testing.T) {
	root := scanRoot(t)
	ref, err := (Adapter{}).Locate(root, "s-1")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Meta.SessionID != "s-1" || len(ref.SubagentPaths) != 1 {
		t.Errorf("ref = %+v", ref)
	}

	if _, err := (Adapter{}).Locate(root, "nope"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing session: err = %v, want ErrNotExist", err)
	}
	if _, err := (Adapter{}).Locate(root, "../evil"); err == nil {
		t.Error("path-escaping session id not rejected")
	}
}

func TestLocateMismatchAndAmbiguity(t *testing.T) {
	root := scanRoot(t)
	mt := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC)
	// A file whose name disagrees with its in-band session id.
	writeFile(t, filepath.Join(root, "-tmp-exp", "s-x.jsonl"),
		ccLine("s-1", "/tmp/exp", "2026-07-20T10:00:00.000Z", false, ""), mt)
	if _, err := (Adapter{}).Locate(root, "s-x"); err == nil || !strings.Contains(err.Error(), "records session id") {
		t.Errorf("mismatch: err = %v", err)
	}
	// The same session id in two project directories.
	writeFile(t, filepath.Join(root, "-tmp-exp2", "s-1.jsonl"),
		ccLine("s-1", "/tmp/exp2", "2026-07-20T10:00:00.000Z", false, ""), mt)
	if _, err := (Adapter{}).Locate(root, "s-1"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("ambiguity: err = %v", err)
	}
}

// TestLocalScan runs the per-file accounting invariant over real
// transcripts: every regular file under the root is a ref path, a subagent
// path, a skip, or a scan error.
func TestLocalScan(t *testing.T) {
	root := os.Getenv("AGENTMINUTES_LOCAL_TRANSCRIPTS")
	if root == "" {
		t.Skip("set AGENTMINUTES_LOCAL_TRANSCRIPTS to run against real transcripts")
	}
	locatetest.Invariant(t, Adapter{}, root)
}
