package antigravity

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/agent-ecosystem/agentminutes/harness"
)

// parentTranscript renders a conversation that spawned children: the
// invoke_subagent result embeds each child's conversation id the way
// 1.2.x writes it.
func parentTranscript(children ...string) string {
	s := userInputLine +
		`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-07-20T10:00:01Z","tool_calls":[{"name":"invoke_subagent","args":{"Subagents":[{"Prompt":"do it","Role":"worker","TypeName":"self"}]}}]}` + "\n"
	content := "Created At: x\\nCompleted At: y\\nCreated the following subagents:\\n"
	for _, c := range children {
		content += `{\n  \"conversationId\": \"` + c + `\",\n  \"logAbsoluteUri\": \"file:///brain/` + c + `/.system_generated/logs/transcript.jsonl\"\n}\n`
	}
	s += `{"step_index":2,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-07-20T10:00:02Z","content":"` + content + `The subagents will send you a message when they have completed."}` + "\n"
	return s
}

// TestGather pins the content join: children named in invoke_subagent
// results are located under root and gathered recursively; ids that do
// not resolve are skipped rather than failing the task.
func TestGather(t *testing.T) {
	root := t.TempDir()
	mt := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC)
	tf := func(conv string) string {
		return filepath.Join(root, conv, ".system_generated", "logs", "transcript_full.jsonl")
	}
	writeFile(t, tf("parent-1"), parentTranscript("child-1", "child-2", "missing-9"), mt)
	writeFile(t, tf("child-1"), parentTranscript("grandchild-1"), mt)
	writeFile(t, tf("child-2"), userInputLine, mt)
	writeFile(t, tf("grandchild-1"), userInputLine, mt)
	writeFile(t, tf("unrelated"), userInputLine, mt)

	a := Adapter{}
	ref, err := a.Locate(root, "parent-1")
	if err != nil {
		t.Fatal(err)
	}
	task, err := a.Gather(root, ref)
	if err != nil {
		t.Fatal(err)
	}
	if task.Join != harness.JoinContent {
		t.Errorf("join = %q", task.Join)
	}
	var got []string
	for _, s := range task.Subagents {
		got = append(got, s.Meta.SessionID)
	}
	if len(task.Skipped) != 1 || task.Skipped[0].Path != "missing-9" {
		t.Errorf("skipped = %+v, want the unresolved child id", task.Skipped)
	}
	want := []string{"child-1", "grandchild-1", "child-2"}
	if len(got) != len(want) {
		t.Fatalf("subagents = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("subagent %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestConversationIDOf(t *testing.T) {
	if got := conversationIDOf(filepath.Join("/brain", "conv-9", ".system_generated", "logs", "transcript_full.jsonl")); got != "conv-9" {
		t.Errorf("conversationIDOf = %q, want conv-9", got)
	}
	if got := conversationIDOf(filepath.Join("/elsewhere", "copy.jsonl")); got != "" {
		t.Errorf("conversationIDOf(flat copy) = %q, want empty", got)
	}
}
