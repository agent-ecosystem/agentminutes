package copilot

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/agent-ecosystem/agentminutes/harness"
)

// TestGather pins the inline join: a Copilot task is its one transcript,
// and the per-agent split lives on the events.
func TestGather(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "sess-a", "events.jsonl"), startLine("sess-a", "/tmp/exp", "2026-09-25T10:00:00.000Z"), time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC))
	a := Adapter{}
	ref, err := a.Locate(root, "sess-a")
	if err != nil {
		t.Fatal(err)
	}
	task, err := a.Gather(root, ref)
	if err != nil {
		t.Fatal(err)
	}
	if task.Join != harness.JoinInline || len(task.Subagents) != 0 || task.Parent.Path != ref.Path {
		t.Errorf("task = %+v", task)
	}
}
