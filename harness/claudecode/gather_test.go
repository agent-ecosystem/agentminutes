package claudecode

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/agent-ecosystem/agentminutes/harness"
)

// TestGather pins the layout join: subagent files under
// <session>/subagents/ belong to the parent, whether the ref came from a
// scan (SubagentPaths filled) or a bare BuildRef from the path.
func TestGather(t *testing.T) {
	root := t.TempDir()
	mt := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	parent := filepath.Join(root, "-tmp-exp", "p-1.jsonl")
	writeFile(t, parent, ccLine("p-1", "/tmp/exp", "2026-09-25T10:00:00.000Z", false, ""), mt)
	writeFile(t, filepath.Join(root, "-tmp-exp", "p-1", "subagents", "agent-a1.jsonl"), ccLine("p-1", "/tmp/exp", "2026-09-25T10:00:01.000Z", true, "a1"), mt)
	writeFile(t, filepath.Join(root, "-tmp-exp", "p-1", "subagents", "agent-a2.jsonl"), ccLine("p-1", "/tmp/exp", "2026-09-25T10:00:02.000Z", true, "a2"), mt)
	writeFile(t, filepath.Join(root, "-tmp-exp", "p-1", "subagents", "agent-a1.meta.json"), "{}", mt)
	writeFile(t, filepath.Join(root, "-tmp-exp", "p-2.jsonl"), ccLine("p-2", "/tmp/exp", "2026-09-25T10:00:03.000Z", false, ""), mt)

	a := Adapter{}
	ref, _, err := harness.BuildRef(a, parent, harness.ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	task, err := a.Gather(root, ref)
	if err != nil {
		t.Fatal(err)
	}
	if task.Join != harness.JoinLayout || len(task.Subagents) != 2 {
		t.Fatalf("task = %+v", task)
	}
	for i, id := range []string{"a1", "a2"} {
		s := task.Subagents[i]
		if s.Meta.SubagentID != id || !s.Meta.IsSubagent || s.Meta.SessionID != "p-1" {
			t.Errorf("subagent %d = %+v", i, s.Meta)
		}
	}
	// A parent with no session directory gathers nothing.
	ref2, err := a.Locate(root, "p-2")
	if err != nil {
		t.Fatal(err)
	}
	if task, err := a.Gather(root, ref2); err != nil || len(task.Subagents) != 0 {
		t.Errorf("p-2 task = %+v, %v", task, err)
	}
}
