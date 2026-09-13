package codex

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/parseutil"
	"github.com/agent-ecosystem/agentminutes/session"
)

// subagent_parent.jsonl and subagent_child.jsonl pin the 0.154.0
// multi-agent rollout shapes: the collaboration function calls
// (spawn_agent/wait_agent, the first observed function_call records),
// SubAgentActivity and CollabAgentToolCall items, agent_message response
// items, the inter_agent_communication_metadata record, and the subagent
// session_meta (thread_source "subagent", source as an object,
// parent_thread_id, agent_path). A subagent rollout records the root
// thread's id as session_id at every spawn depth, so SessionID groups a
// task's files; the child's own thread id is its filename id and
// SubagentID.
func parseSubagentFixture(t *testing.T, name string) (*session.Session, []byte, []int) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var skips []int
	s, err := harness.Parse(Adapter{}, bytes.NewReader(data), harness.Options{
		OnSkip: func(line int, _ string) { skips = append(skips, line) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, data, skips
}

func TestSubagentParentFixture(t *testing.T) {
	s, data, skips := parseSubagentFixture(t, "subagent_parent.jsonl")

	if m := s.Meta; m.SessionID != "sess-codex-root" || m.IsSubagent || m.SubagentID != "" {
		t.Errorf("Meta = %+v, want plain root-session identity", m)
	}

	var toolNames []string
	agentMsgs := 0
	for _, ev := range s.Events {
		switch ev.Kind {
		case session.KindToolCall:
			toolNames = append(toolNames, ev.ToolCall.Name)
		case session.KindSystem:
			if ev.System.Subtype == "agent_message" {
				agentMsgs++
			}
		}
	}
	if len(toolNames) != 2 || toolNames[0] != "spawn_agent" || toolNames[1] != "wait_agent" {
		t.Errorf("tool calls = %v, want [spawn_agent wait_agent]", toolNames)
	}
	if agentMsgs != 1 {
		t.Errorf("agent_message system events = %d, want 1", agentMsgs)
	}
	for _, ti := range s.ToolInteractions() {
		if len(ti.Results) != 1 {
			t.Errorf("%s: %d results, want the function_call_output paired", ti.Call.ToolCall.Name, len(ti.Results))
		}
	}

	if s.Totals == nil || s.Totals.InputTokens != 30000 || s.Totals.OutputTokens != 90 ||
		s.Totals.CacheReadInputTokens != 19000 || s.Totals.CacheCreationInputTokens != 10900 {
		t.Errorf("Totals = %+v, want 30000/90/19000/10900", s.Totals)
	}
	if un := parseutil.UncoveredLines(data, s.Events, skips); len(un) > 0 {
		t.Errorf("lines not covered: %v", un)
	}
}

func TestSubagentChildFixture(t *testing.T) {
	s, data, skips := parseSubagentFixture(t, "subagent_child.jsonl")

	m := s.Meta
	if m.SessionID != "sess-codex-root" || m.SubagentID != "sess-codex-sub1" || !m.IsSubagent {
		t.Errorf("Meta = %+v, want parent session_id with subagent identity", m)
	}

	agentMsgs := 0
	for _, ev := range s.Events {
		if ev.Kind == session.KindSystem && ev.System.Subtype == "agent_message" {
			agentMsgs++
			if len(ev.System.Details) == 0 {
				t.Error("agent_message system event lost its payload")
			}
		}
	}
	if agentMsgs != 1 {
		t.Errorf("agent_message system events = %d, want the inbound TASK message", agentMsgs)
	}

	if s.Totals == nil || s.Totals.InputTokens != 12000 || s.Totals.OutputTokens != 5 {
		t.Errorf("Totals = %+v, want 12000/5", s.Totals)
	}
	if un := parseutil.UncoveredLines(data, s.Events, skips); len(un) > 0 {
		t.Errorf("lines not covered: %v", un)
	}
}
