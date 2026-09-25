package codex

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-ecosystem/agentminutes/harness"
)

func subagentLine(rootID, threadID, ts string) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"session_id":%q,"id":%q,"timestamp":%q,"cwd":"/tmp/exp","cli_version":"0.157.0","thread_source":"subagent","parent_thread_id":%q,"agent_path":"/root/child"}}`,
		ts, rootID, threadID, ts, rootID) + "\n"
}

// TestGather pins the session_id join: sibling rollouts whose session_id
// is the parent's root id (at any depth) are the task; other sessions,
// older rollouts, and the parent itself are not.
func TestGather(t *testing.T) {
	root := t.TempDir()
	mt := func(h int) time.Time { return time.Date(2026, 9, 25, h, 0, 0, 0, time.UTC) }
	day := filepath.Join(root, "2026", "09", "25")
	writeFile(t, filepath.Join(day, "rollout-2026-09-25T10-00-00-p-1.jsonl"), metaLine("p-1", "/tmp/exp", "2026-09-25T10:00:00.000Z"), mt(10))
	writeFile(t, filepath.Join(day, "rollout-2026-09-25T10-01-00-c-1.jsonl"), subagentLine("p-1", "c-1", "2026-09-25T10:01:00.000Z"), mt(10))
	writeFile(t, filepath.Join(day, "rollout-2026-09-25T10-02-00-c-2.jsonl"), subagentLine("p-1", "c-2", "2026-09-25T10:02:00.000Z"), mt(10))
	writeFile(t, filepath.Join(day, "rollout-2026-09-25T10-03-00-p-2.jsonl"), metaLine("p-2", "/tmp/exp", "2026-09-25T10:03:00.000Z"), mt(10))
	writeFile(t, filepath.Join(day, "rollout-2026-09-25T10-04-00-c-9.jsonl"), subagentLine("p-2", "c-9", "2026-09-25T10:04:00.000Z"), mt(10))

	a := Adapter{}
	ref, err := a.Locate(root, "p-1")
	if err != nil {
		t.Fatal(err)
	}
	task, err := a.Gather(root, ref)
	if err != nil {
		t.Fatal(err)
	}
	if task.Join != harness.JoinSessionID || len(task.Subagents) != 2 {
		t.Fatalf("task = %+v", task)
	}
	for i, id := range []string{"c-1", "c-2"} {
		if s := task.Subagents[i]; s.Meta.SubagentID != id || s.Meta.SessionID != "p-1" {
			t.Errorf("subagent %d = %+v", i, s.Meta)
		}
	}
	// A subagent gathers nothing: the task is grouped by its root.
	child, err := a.Locate(root, "c-1")
	if err != nil {
		t.Fatal(err)
	}
	if task, err := a.Gather(root, child); err != nil || len(task.Subagents) != 0 {
		t.Errorf("child task = %+v, %v", task, err)
	}
}

// TestGatherReportsUnreadable pins that a candidate rollout the gather
// cannot read is listed in Task.Skipped rather than silently omitted.
func TestGatherReportsUnreadable(t *testing.T) {
	root := t.TempDir()
	mt := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	day := filepath.Join(root, "2026", "09", "25")
	writeFile(t, filepath.Join(day, "rollout-2026-09-25T10-00-00-p-1.jsonl"), metaLine("p-1", "/tmp/exp", "2026-09-25T10:00:00.000Z"), mt)
	writeFile(t, filepath.Join(day, "rollout-2026-09-25T10-01-00-bad.jsonl"), "not json\n", mt)
	a := Adapter{}
	ref, err := a.Locate(root, "p-1")
	if err != nil {
		t.Fatal(err)
	}
	task, err := a.Gather(root, ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(task.Skipped) != 1 || !strings.Contains(task.Skipped[0].Path, "bad") || !strings.Contains(task.Skipped[0].Reason, "unreadable") {
		t.Errorf("skipped = %+v", task.Skipped)
	}
}
