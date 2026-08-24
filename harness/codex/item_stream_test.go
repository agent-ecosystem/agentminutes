package codex

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

// item_stream.jsonl pins the 0.149.1 rollout shape: an ordinal on every
// record, and the event_msg item_completed stream (typed items) replacing
// the user_message/agent_message/patch_apply_end/web_search_end telemetry.
func parseItemStreamFixture(t *testing.T, transforms ...session.Transform) (*session.Session, []byte, []int) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "item_stream.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var skips []int
	s, err := harness.Parse(Adapter{}, bytes.NewReader(data), harness.Options{
		OnSkip: func(line int, _ string) { skips = append(skips, line) },
	}, transforms...)
	if err != nil {
		t.Fatal(err)
	}
	return s, data, skips
}

func TestItemStreamFixtureEventSequence(t *testing.T) {
	s, data, skips := parseItemStreamFixture(t)

	want := []session.EventKind{
		session.KindSessionMeta,      // L1
		session.KindSystem,           // task_started, L2
		session.KindSystem,           // turn_context, L3
		session.KindUserMessage,      // human prompt, L4
		session.KindSystem,           // item_completed UserMessage, L5
		session.KindThinking,         // L6
		session.KindSystem,           // item_completed Reasoning, L7
		session.KindToolCall,         // exec (shell), L8
		session.KindSystem,           // item_completed CommandExecution, L9
		session.KindToolResult,       // L10
		session.KindSystem,           // token_count, L11
		session.KindToolCall,         // exec (apply_patch script), L12
		session.KindSystem,           // item_completed FileChange, L13
		session.KindToolResult,       // L14
		session.KindSystem,           // token_count, L15
		session.KindToolCall,         // exec (web__run script), L16
		session.KindSystem,           // item_completed Extension, L17
		session.KindToolResult,       // L18
		session.KindSystem,           // token_count, L19
		session.KindSystem,           // item_completed AgentMessage, L21
		session.KindSystem,           // token_count, L22
		session.KindAssistantMessage, // anchor (L20), closed by task_complete
		session.KindSystem,           // task_complete, L23
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

	if m := s.Meta; m.HarnessVersion != "0.149.1" || m.SessionID != "sess-codex-2" {
		t.Errorf("Meta = %+v", m)
	}
	// Pooled per-request usage: 9000/50 + 8000/60 + 7000/40 + 6000/80.
	if s.Totals == nil || s.Totals.InputTokens != 30000 || s.Totals.OutputTokens != 230 ||
		s.Totals.CacheReadInputTokens != 3000 || s.Totals.CacheCreationInputTokens != 7850 {
		t.Errorf("Totals = %+v, want 30000/230/3000/7850", s.Totals)
	}
	// Codex input_tokens is already the total prompt (cache fields are
	// subsets), so the derived comparable total equals the input sum.
	if s.Totals != nil && s.Totals.TotalPromptTokens != 30000 {
		t.Errorf("TotalPromptTokens = %d, want 30000", s.Totals.TotalPromptTokens)
	}
	if un := parseutil.UncoveredLines(data, s.Events, skips); len(un) > 0 {
		t.Errorf("lines not covered by any event or skip: %v", un)
	}
}

// TestItemStreamNoNativeEditOrFetch pins why the promotion transforms
// exist on 0.149.1: every native tool call is the unified exec wrapper
// (kind execute), so edits and fetches are invisible without promotion.
func TestItemStreamNoNativeEditOrFetch(t *testing.T) {
	s, _, _ := parseItemStreamFixture(t)
	for _, ti := range s.ToolInteractions() {
		if k := ti.Call.ToolCall.Kind; k != session.ToolKindExecute {
			t.Errorf("native tool call kind %q, want everything to be the exec wrapper", k)
		}
	}
}

func TestItemStreamPromotePatchApply(t *testing.T) {
	s, data, skips := parseItemStreamFixture(t, PromotePatchApply)

	var promoted *session.ToolCall
	for _, ti := range s.ToolInteractions() {
		if ti.Call.ToolCall.PromotedFrom != "" {
			if promoted != nil {
				t.Fatal("more than one promoted call; only the FileChange item should promote")
			}
			promoted = ti.Call.ToolCall
			if len(ti.Results) != 1 || ti.Results[0].ToolResult.IsError {
				t.Errorf("promoted results = %+v, want one clean result", ti.Results)
			}
		}
	}
	if promoted == nil {
		t.Fatal("no promoted edit pair")
	}
	if promoted.ToolCallID != "exec-patch-1" || promoted.Name != "apply_patch" ||
		promoted.Kind != session.ToolKindEdit ||
		promoted.PromotedFrom != "event_msg/item_completed/FileChange" {
		t.Errorf("promoted call = %+v", promoted)
	}
	if !strings.Contains(string(promoted.Input), "/tmp/exp/hello.txt") {
		t.Errorf("promoted input = %s, want the changes map", promoted.Input)
	}

	// The other item_completed records stay system telemetry.
	items := 0
	for i := range s.Events {
		if s.Events[i].Kind == session.KindSystem && s.Events[i].System.Subtype == "item_completed" {
			items++
		}
	}
	if items != 5 {
		t.Errorf("remaining item_completed system events = %d, want 5 (only FileChange replaced)", items)
	}
	if un := parseutil.UncoveredLines(data, s.Events, skips); len(un) > 0 {
		t.Errorf("lines not covered with transform: %v", un)
	}
}

func TestItemStreamPromoteWebSearch(t *testing.T) {
	s, data, skips := parseItemStreamFixture(t, PromoteWebSearch)

	var promoted *session.ToolCall
	for _, ti := range s.ToolInteractions() {
		if ti.Call.ToolCall.PromotedFrom != "" {
			if promoted != nil {
				t.Fatal("more than one promoted call; only the Extension web.search item should promote")
			}
			promoted = ti.Call.ToolCall
		}
	}
	if promoted == nil {
		t.Fatal("no promoted fetch pair")
	}
	if promoted.ToolCallID != "exec-web-1" || promoted.Name != "web_search" ||
		promoted.Kind != session.ToolKindFetch ||
		promoted.PromotedFrom != "event_msg/item_completed/Extension" {
		t.Errorf("promoted call = %+v", promoted)
	}
	if !strings.Contains(string(promoted.Input), "openPage") {
		t.Errorf("promoted input = %s, want the action", promoted.Input)
	}
	if un := parseutil.UncoveredLines(data, s.Events, skips); len(un) > 0 {
		t.Errorf("lines not covered with transform: %v", un)
	}
}

// TestPromoteFileChangeItemFailure pins the status-derived error flag on
// the 0.149+ shape.
func TestPromoteFileChangeItemFailure(t *testing.T) {
	input := `{"timestamp":"2026-08-24T04:00:00.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"s","cli_version":"0.149.1","cwd":"/tmp"}}` + "\n" +
		`{"timestamp":"2026-08-24T04:00:01.000Z","ordinal":1,"type":"event_msg","payload":{"type":"item_completed","item":{"type":"FileChange","changes":{"/tmp/x":{"type":"update"}},"status":"failed","stderr":"conflict"}}}` + "\n"
	s, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{}, PromotePatchApply)
	if err != nil {
		t.Fatal(err)
	}
	ti := s.ToolInteractions()
	if len(ti) != 1 || !ti[0].Results[0].ToolResult.IsError {
		t.Fatalf("interactions = %+v, want a failed pair", ti)
	}
	if ti[0].Call.ToolCall.ToolCallID != "item_completed:L2" {
		t.Fatalf("id = %q, want line-derived ID without item id", ti[0].Call.ToolCall.ToolCallID)
	}
}

// TestPromoteIgnoresOtherItems pins that promotion never touches the
// item_completed records that duplicate response_items (messages,
// reasoning, command executions).
func TestPromoteIgnoresOtherItems(t *testing.T) {
	input := `{"timestamp":"2026-08-24T04:00:00.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"s","cli_version":"0.149.1","cwd":"/tmp"}}` + "\n" +
		`{"timestamp":"2026-08-24T04:00:01.000Z","ordinal":1,"type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"exec-1","status":"completed"}}}` + "\n" +
		`{"timestamp":"2026-08-24T04:00:02.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","item":{"type":"Extension","kind":"browser.render","id":"exec-2"}}}` + "\n"
	for _, tr := range []session.Transform{PromotePatchApply, PromoteWebSearch} {
		s, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{}, tr)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(s.ToolInteractions()); n != 0 {
			t.Errorf("interactions = %d, want 0 (nothing promotable)", n)
		}
	}
}
