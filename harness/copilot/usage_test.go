package copilot

import (
	"strings"
	"testing"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/parseutil"
	"github.com/agent-ecosystem/agentminutes/session"
)

// The usage fixture models a 1.0.105 session: a session.usage_record ahead
// of every assistant.message, matched by apiCallId (one unclaimed record,
// the session-title helper call), plus the interactive-session shapes the
// corpus added since 1.0.88: hook.start/hook.end around the prompt (the
// hook's additionalContext is model-visible), the rg and apply_patch
// tools, a helper-model call under model.*, subagent.deselected, and a
// compaction whose summary replaces the removed messages.
func TestUsageFixture(t *testing.T) {
	s, data, skips := parseNamed(t, "usage.jsonl", harness.Options{})
	if len(skips) != 0 {
		t.Errorf("skips = %v, want none", skips)
	}
	if un := parseutil.UncoveredLines(data, s.Events, skips); len(un) > 0 {
		t.Errorf("lines not covered: %v", un)
	}
	if s.Meta.HarnessVersion != "1.0.105" {
		t.Errorf("version = %q", s.Meta.HarnessVersion)
	}

	var anchors []*session.AssistantMessage
	for _, ev := range s.Events {
		if ev.Kind == session.KindAssistantMessage {
			anchors = append(anchors, ev.AssistantMessage)
		}
	}
	if len(anchors) != 3 {
		t.Fatalf("got %d anchors, want 3", len(anchors))
	}
	want := []session.TokenUsage{
		{InputTokens: 25779, OutputTokens: 4, CacheCreationInputTokens: 25775},
		{InputTokens: 25834, OutputTokens: 40, CacheReadInputTokens: 25775, CacheCreationInputTokens: 55},
		{InputTokens: 25900, OutputTokens: 12, CacheReadInputTokens: 25830, CacheCreationInputTokens: 70},
	}
	for i, am := range anchors {
		if am.Usage == nil {
			t.Errorf("anchor %d: no usage", i)
			continue
		}
		if u := am.Usage; u.InputTokens != want[i].InputTokens || u.OutputTokens != want[i].OutputTokens ||
			u.CacheReadInputTokens != want[i].CacheReadInputTokens || u.CacheCreationInputTokens != want[i].CacheCreationInputTokens {
			t.Errorf("anchor %d usage = %+v, want %+v", i, u, want[i])
		}
		if string(am.Usage.Extra["reasoningTokens"]) != "0" {
			t.Errorf("anchor %d reasoningTokens = %s", i, am.Usage.Extra["reasoningTokens"])
		}
	}
	// Totals sum the claimed records only (the helper call's 300 input
	// tokens stay in its system event); inputTokens is already the total
	// prompt, so the comparable total equals it.
	if s.Totals == nil || s.Totals.InputTokens != 77513 || s.Totals.OutputTokens != 56 ||
		s.Totals.CacheReadInputTokens != 51605 || s.Totals.CacheCreationInputTokens != 25900 ||
		s.Totals.TotalPromptTokens != 77513 {
		t.Errorf("Totals = %+v", s.Totals)
	}

	st := s.Stats()
	if st.ToolCallsByKind[session.ToolKindSearch] != 1 || st.ToolCallsByKind[session.ToolKindEdit] != 1 ||
		st.SystemBySubtype["session.usage_record"] != 4 || st.SystemBySubtype["hook.end"] != 1 ||
		st.SystemBySubtype["model.messages_snapshot"] != 1 || st.SystemBySubtype["subagent.deselected"] != 1 {
		t.Errorf("stats = kinds %+v subtypes %+v", st.ToolCallsByKind, st.SystemBySubtype)
	}
	for _, ev := range s.Events {
		if ev.Kind != session.KindSystem {
			continue
		}
		switch ev.System.Subtype {
		case "hook.end":
			if !strings.HasPrefix(ev.System.Text, "<workspace_info>\n") {
				t.Errorf("hook.end text = %q", ev.System.Text)
			}
		case "hook.start", "session.usage_record", "model.messages_snapshot", "model.response":
			if ev.System.Text != "" {
				t.Errorf("%s surfaces text %q, want none", ev.System.Subtype, ev.System.Text)
			}
		case "session.compaction_complete":
			if !strings.HasPrefix(ev.System.Text, "<overview>\n") {
				t.Errorf("compaction text = %q", ev.System.Text)
			}
		}
	}
}
