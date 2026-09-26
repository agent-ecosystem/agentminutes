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

// usage_record.jsonl pins the 0.154.0 rollout shape: the token_usage_record
// top-level record (per-response usage telemetry duplicating the event_msg
// token_count stream) and the root_turn_id key on turn_context.
func parseUsageRecordFixture(t *testing.T) (*session.Session, []byte, []int) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "usage_record.jsonl"))
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

func TestUsageRecordFixtureEventSequence(t *testing.T) {
	s, data, skips := parseUsageRecordFixture(t)

	want := []session.EventKind{
		session.KindSessionMeta,      // L1
		session.KindSystem,           // session_meta/base_instructions, L1
		session.KindSystem,           // task_started, L2
		session.KindSystem,           // turn_context, L3
		session.KindUserMessage,      // L4
		session.KindSystem,           // item_completed UserMessage, L5
		session.KindSystem,           // token_usage_record, L7
		session.KindSystem,           // token_count, L8
		session.KindAssistantMessage, // anchor (L6), closed by task_complete
		session.KindSystem,           // task_complete, L9
	}
	got := make([]session.EventKind, len(s.Events))
	for i, ev := range s.Events {
		got[i] = ev.Kind
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events %v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d: kind %q, want %q", i, got[i], want[i])
		}
	}

	if m := s.Meta; m.HarnessVersion != "0.154.0" || m.SessionID != "sess-codex-3" {
		t.Errorf("Meta = %+v", m)
	}
	// Usage comes from the token_count event alone; the token_usage_record
	// carrying the same numbers must not double them.
	if s.Totals == nil || s.Totals.InputTokens != 1200 || s.Totals.OutputTokens != 55 ||
		s.Totals.CacheReadInputTokens != 200 || s.Totals.CacheCreationInputTokens != 900 {
		t.Errorf("Totals = %+v, want 1200/55/200/900", s.Totals)
	}
	if un := parseutil.UncoveredLines(data, s.Events, skips); len(un) > 0 {
		t.Errorf("lines not covered by any event or skip: %v", un)
	}
}

func TestUsageRecordSystemSubtype(t *testing.T) {
	s, _, _ := parseUsageRecordFixture(t)

	var found bool
	for _, ev := range s.Events {
		if ev.Kind == session.KindSystem && ev.System != nil && ev.System.Subtype == "token_usage_record" {
			found = true
			if len(ev.System.Details) == 0 {
				t.Error("token_usage_record system event has no Details payload")
			}
		}
	}
	if !found {
		t.Error("no system event with subtype token_usage_record")
	}
}
