package antigravity

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/parseutil"
	"github.com/agent-ecosystem/agentminutes/session"
)

func parseNamed(t *testing.T, name string) (*session.Session, []byte) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	s, err := harness.Parse(Adapter{}, bytes.NewReader(data), harness.Options{})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if un := parseutil.UncoveredLines(data, s.Events, nil); len(un) > 0 {
		t.Errorf("%s: lines not covered: %v", name, un)
	}
	return s, data
}

// The usage fixture pins the 1.4.12 shapes: per-response usage on
// PLANNER_RESPONSE (input_tokens the uncached remainder, cache_read_tokens
// disjoint from it, output_tokens), the renamed write_file and read_file
// tools, the Created At/Completed At result header with the "Command
// exited with code N." template, and the subagent round trip (an
// invoke_subagent result naming the child, the child's report arriving as
// a SYSTEM_MESSAGE).
func TestUsageFixture(t *testing.T) {
	s, _ := parseNamed(t, "usage.jsonl")

	var anchors []*session.AssistantMessage
	for _, ev := range s.Events {
		if ev.Kind == session.KindAssistantMessage {
			anchors = append(anchors, ev.AssistantMessage)
		}
	}
	if len(anchors) != 5 {
		t.Fatalf("got %d anchors, want 5", len(anchors))
	}
	want := []session.TokenUsage{
		{InputTokens: 7000, OutputTokens: 40},
		{InputTokens: 2200, OutputTokens: 60, CacheReadInputTokens: 5000},
		{InputTokens: 2300, OutputTokens: 50, CacheReadInputTokens: 5300},
		{InputTokens: 2400, OutputTokens: 20, CacheReadInputTokens: 5500},
		{InputTokens: 2500, OutputTokens: 10, CacheReadInputTokens: 5600},
	}
	for i, am := range anchors {
		if am.Usage == nil || am.Usage.InputTokens != want[i].InputTokens || am.Usage.OutputTokens != want[i].OutputTokens ||
			am.Usage.CacheReadInputTokens != want[i].CacheReadInputTokens || am.Usage.CacheCreationInputTokens != 0 || am.Usage.Extra != nil {
			t.Errorf("anchor %d usage = %+v, want %+v", i, am.Usage, want[i])
		}
		if am.Model != "Gemini 4 Argon (Medium)" {
			t.Errorf("anchor %d model = %q", i, am.Model)
		}
	}
	// Totals sum the per-message usage; the comparable prompt total adds
	// the disjoint cache reads (the Anthropic-style convention).
	if s.Totals == nil || s.Totals.InputTokens != 16400 || s.Totals.OutputTokens != 180 ||
		s.Totals.CacheReadInputTokens != 21400 || s.Totals.CacheCreationInputTokens != 0 ||
		s.Totals.TotalPromptTokens != 37800 {
		t.Errorf("Totals = %+v, want 16400/180/21400/0, prompt 37800", s.Totals)
	}

	st := s.Stats()
	if st.ToolCallsByKind[session.ToolKindEdit] != 1 || st.ToolCallsByKind[session.ToolKindRead] != 1 ||
		st.ToolCallsByKind[session.ToolKindExecute] != 1 || st.ToolCallsByName["invoke_subagent"] != 1 {
		t.Errorf("tool kinds = %+v names = %+v", st.ToolCallsByKind, st.ToolCallsByName)
	}
	for _, ti := range s.ToolInteractions() {
		call, res := ti.Call.ToolCall, ti.Results[0].ToolResult
		switch call.Name {
		case "run_command":
			// The 1.4.12 template ("Command exited with code 1.") still
			// classifies a nonzero exit as a failed call.
			if !res.IsError || !strings.Contains(res.Text(), "cat: missing.txt") {
				t.Errorf("run_command result = %+v", res)
			}
		case "write_file":
			if res.IsError || !strings.HasSuffix(res.Text(), "Created file.") {
				t.Errorf("write_file result = %+v", res)
			}
		case "read_file":
			if !res.IsError || !strings.Contains(res.Text(), "file does not exist") {
				t.Errorf("read_file result = %+v", res)
			}
		case "invoke_subagent":
			if res.IsError || !strings.Contains(res.Text(), `"conversationId":  "c0ffee00-1111-4222-8333-444455556666"`) {
				t.Errorf("invoke_subagent result = %+v", res)
			}
		}
	}
	if st.UnansweredCalls != 0 || st.OrphanResults != 0 {
		t.Errorf("unanswered %d orphans %d", st.UnansweredCalls, st.OrphanResults)
	}
}

// The subagent_child fixture pins the 1.4.12 child conversation: no
// USER_INPUT at all (the delegated prompt arrives as a SYSTEM_MESSAGE from
// the parent, so the model label is unknown and no user_message is
// emitted), per-response usage, and the send_message report back.
func TestSubagentChildFixture(t *testing.T) {
	s, _ := parseNamed(t, "subagent_child.jsonl")
	st := s.Stats()
	if st.UserMessages != 0 || st.EventCounts[session.KindAssistantMessage] != 3 || st.ToolCallsByName["send_message"] != 1 || st.ToolCallsByName["run_command"] != 1 {
		t.Errorf("stats = %+v", st)
	}
	if s.Events[1].Kind != session.KindSystem || s.Events[1].System.Subtype != "system_message" ||
		!strings.Contains(s.Events[1].System.Text, "sender=aa11bb22-3333-4444-8555-666677778888") {
		t.Errorf("first record = %+v", s.Events[1])
	}
	if s.Totals == nil || s.Totals.InputTokens != 11645 || s.Totals.CacheReadInputTokens != 10390 || s.Totals.TotalPromptTokens != 22035 {
		t.Errorf("Totals = %+v", s.Totals)
	}
	for _, ev := range s.Events {
		if ev.Kind == session.KindAssistantMessage && ev.AssistantMessage.Model != "" {
			t.Errorf("model = %q, want unknown (no settings change in a child)", ev.AssistantMessage.Model)
		}
	}
}

// Records without the 1.4.12 usage keys keep nil usage and nil totals: an
// absent key is not a measured zero.
func TestUsageAbsentBefore1412(t *testing.T) {
	s := parseFixture(t, harness.Options{})
	if s.Totals != nil {
		t.Errorf("Totals = %+v, want nil for the pre-1.4.12 fixture", s.Totals)
	}
}
