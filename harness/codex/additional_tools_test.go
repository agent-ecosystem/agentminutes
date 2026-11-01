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

// additional_tools.jsonl pins the 0.166.0 rollout shape: the
// additional_tools response item (the developer-role tool catalog that
// opens every thread), turn_attribution on task_started, retained_source
// in message metadata, and cyber_access_program on turn_context.
func TestAdditionalToolsFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "additional_tools.jsonl"))
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
	want := []session.EventKind{
		session.KindSessionMeta,      // L1
		session.KindSystem,           // session_meta/base_instructions, L1
		session.KindSystem,           // task_started, L2
		session.KindSystem,           // additional_tools, L3
		session.KindSystem,           // turn_context, L4
		session.KindUserMessage,      // L5
		session.KindSystem,           // item_completed UserMessage, L6
		session.KindSystem,           // item_completed AgentMessage, L8
		session.KindSystem,           // token_usage_record, L9
		session.KindSystem,           // token_count, L10
		session.KindAssistantMessage, // anchor (L7), closed by task_complete
		session.KindSystem,           // task_complete, L11
	}
	if len(s.Events) != len(want) {
		t.Fatalf("got %d events, want %d", len(s.Events), len(want))
	}
	for i := range want {
		if s.Events[i].Kind != want[i] {
			t.Errorf("event %d: kind %q, want %q", i, s.Events[i].Kind, want[i])
		}
	}
	at := s.Events[3].System
	if at.Subtype != "additional_tools" || at.Text != "" || len(at.Details) == 0 {
		t.Errorf("additional_tools event = %+v", at)
	}
	if m := s.Meta; m.HarnessVersion != "0.166.0" || m.SessionID != "sess-codex-tools" {
		t.Errorf("Meta = %+v", m)
	}
	if s.Totals == nil || s.Totals.InputTokens != 1300 || s.Totals.OutputTokens != 5 || s.Totals.CacheCreationInputTokens != 1296 {
		t.Errorf("Totals = %+v", s.Totals)
	}
	if un := parseutil.UncoveredLines(data, s.Events, skips); len(un) > 0 {
		t.Errorf("lines not covered: %v", un)
	}
}
