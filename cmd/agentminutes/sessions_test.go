package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-ecosystem/agentminutes/harness"
)

func ccLine(sessionID, cwd, ts string, sidechain bool) string {
	return fmt.Sprintf(`{"parentUuid":null,"isSidechain":%v,"type":"user","message":{"role":"user","content":"hello"},"uuid":"u-1","timestamp":%q,"userType":"external","entrypoint":"cli","cwd":%q,"sessionId":%q,"version":"2.1.197","gitBranch":"main"}`,
		sidechain, ts, cwd, sessionID) + "\n"
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func sessionsRoot(t *testing.T) string {
	t.Helper()
	return writeTree(t, map[string]string{
		filepath.Join("-tmp-exp", "s-1.jsonl"):                          ccLine("s-1", "/tmp/exp", "2026-07-20T10:00:00.000Z", false),
		filepath.Join("-tmp-exp", "s-1", "subagents", "agent-a1.jsonl"): ccLine("s-1", "/tmp/exp", "2026-07-20T10:01:00.000Z", true),
		filepath.Join("-tmp-exp2", "s-2.jsonl"):                         ccLine("s-2", "/tmp/exp2", "2026-07-24T10:00:00.000Z", false),
		filepath.Join("-tmp-exp", "notes.txt"):                          "x",
	})
}

func TestSessionsPaths(t *testing.T) {
	root := sessionsRoot(t)
	stdout, stderr, err := run(t, "sessions", "--harness", "claude-code", "--root", root)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("stdout = %q, want 3 paths", stdout)
	}
	for i, suffix := range []string{"s-1.jsonl", "agent-a1.jsonl", "s-2.jsonl"} {
		if !strings.HasSuffix(lines[i], suffix) {
			t.Errorf("line %d = %q, want suffix %q", i, lines[i], suffix)
		}
	}
	if !strings.Contains(stderr, "2 sessions, 0 filtered out, 1 skipped file, 0 errors") {
		t.Errorf("stderr = %q, want summary", stderr)
	}
}

func TestSessionsCwdFilter(t *testing.T) {
	root := sessionsRoot(t)
	stdout, stderr, err := run(t, "sessions", "--harness", "claude-code", "--root", root, "--cwd", "/tmp/exp")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout, "s-2.jsonl") || !strings.Contains(stdout, "s-1.jsonl") {
		t.Errorf("stdout = %q, want only s-1 and its subagent", stdout)
	}
	if !strings.Contains(stderr, "1 session, 1 filtered out") {
		t.Errorf("stderr = %q, want filter accounting", stderr)
	}
}

func TestSessionsSessionID(t *testing.T) {
	root := sessionsRoot(t)
	stdout, _, err := run(t, "sessions", "--harness", "claude-code", "--root", root, "--session-id", "s-2")
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n"); len(lines) != 1 || !strings.HasSuffix(lines[0], "s-2.jsonl") {
		t.Errorf("stdout = %q, want just s-2.jsonl", stdout)
	}

	stdout, stderr, err := run(t, "sessions", "--harness", "claude-code", "--root", root, "--session-id", "absent")
	if err != nil || stdout != "" {
		t.Errorf("missing id: stdout = %q, err = %v; want empty success", stdout, err)
	}
	if !strings.Contains(stderr, "0 sessions") {
		t.Errorf("stderr = %q, want zero-match summary", stderr)
	}
}

func TestSessionsUntil(t *testing.T) {
	root := sessionsRoot(t)
	// Both fixture sessions start on 2026-07-20 or later; an until before
	// that excludes them regardless of checkout mtimes.
	stdout, stderr, err := run(t, "sessions", "--harness", "claude-code", "--root", root, "--until", "2026-07-19")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "" || !strings.Contains(stderr, "0 sessions") {
		t.Errorf("stdout = %q, stderr = %q; want none before the window", stdout, stderr)
	}
	if _, _, err := run(t, "sessions", "--harness", "claude-code", "--root", root, "--until", "not-a-time"); err == nil {
		t.Error("invalid --until accepted")
	}
}

func TestSessionsJSON(t *testing.T) {
	root := sessionsRoot(t)
	stdout, _, err := run(t, "sessions", "--harness", "claude-code", "--root", root, "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var refs []harness.SessionRef
	if err := json.Unmarshal([]byte(stdout), &refs); err != nil {
		t.Fatalf("output is not a ref array: %v", err)
	}
	if len(refs) != 2 || refs[0].Meta.SessionID != "s-1" || len(refs[0].SubagentPaths) != 1 {
		t.Errorf("refs = %+v", refs)
	}
}

func TestSessionsScanErrorExitCode(t *testing.T) {
	root := writeTree(t, map[string]string{
		filepath.Join("-tmp-exp", "bad.jsonl"): "not json\n",
	})
	_, stderr, err := run(t, "sessions", "--harness", "claude-code", "--root", root)
	var ee *exitError
	if !errors.As(err, &ee) || ee.code != 1 {
		t.Fatalf("err = %v, want exit code 1", err)
	}
	if !strings.Contains(stderr, "bad.jsonl") || !strings.Contains(stderr, "1 error") {
		t.Errorf("stderr = %q, want the per-file error and its count", stderr)
	}
}

func TestSessionsFlagValidation(t *testing.T) {
	if _, _, err := run(t, "sessions", "--root", "/tmp"); err == nil || !strings.Contains(err.Error(), "--root requires --harness") {
		t.Errorf("err = %v, want --root requires --harness", err)
	}
	if _, _, err := run(t, "sessions", "--harness", "claude-code", "--root", t.TempDir(), "--format", "yaml"); err == nil || !strings.Contains(err.Error(), "unknown format") {
		t.Errorf("err = %v, want unknown format", err)
	}
	if _, _, err := run(t, "sessions", "--harness", "not-a-harness"); err == nil {
		t.Errorf("err = %v, want unsupported harness", err)
	}
}

// TestSessionsErrorExitIsSilent pins that the exit-1 sentinel does not make
// cobra append a junk "Error: exit code 1" line after the summary.
func TestSessionsErrorExitIsSilent(t *testing.T) {
	root := writeTree(t, map[string]string{
		filepath.Join("-tmp-exp", "bad.jsonl"): "not json\n",
	})
	_, stderr, err := run(t, "sessions", "--harness", "claude-code", "--root", root)
	var ee *exitError
	if !errors.As(err, &ee) {
		t.Fatalf("err = %v, want exit sentinel", err)
	}
	if strings.Contains(stderr, "Error:") {
		t.Errorf("stderr = %q, want no cobra error line", stderr)
	}
}
