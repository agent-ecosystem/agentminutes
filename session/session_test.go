package session

import (
	"encoding/json"
	"testing"
)

func metaEvent() Event {
	return Event{Kind: KindSessionMeta, SessionMeta: &Meta{
		Harness:        "claude-code",
		HarnessVersion: "2.1.197",
		SessionID:      "s-1",
	}}
}

func TestEventValidate(t *testing.T) {
	ok := Event{Kind: KindThinking, Thinking: &Thinking{Text: "hm"}}
	if err := ok.Validate(); err != nil {
		t.Errorf("valid event: %v", err)
	}

	none := Event{Kind: KindThinking}
	if err := none.Validate(); err == nil {
		t.Error("event with no payload: want error")
	}

	mismatched := Event{Kind: KindThinking, ToolCall: &ToolCall{ToolCallID: "t1"}}
	if err := mismatched.Validate(); err == nil {
		t.Error("kind/payload mismatch: want error")
	}

	double := Event{
		Kind:     KindThinking,
		Thinking: &Thinking{Text: "hm"},
		ToolCall: &ToolCall{ToolCallID: "t1"},
	}
	if err := double.Validate(); err == nil {
		t.Error("event with two payloads: want error")
	}

	emptyCallID := Event{Kind: KindToolCall, ToolCall: &ToolCall{Name: "Read"}}
	if err := emptyCallID.Validate(); err == nil {
		t.Error("tool_call with empty tool_call_id: want error")
	}
	emptyResultID := Event{Kind: KindToolResult, ToolResult: &ToolResult{ToolName: "Read"}}
	if err := emptyResultID.Validate(); err == nil {
		t.Error("tool_result with empty tool_call_id: want error")
	}
}

func TestZeroValueAccumulator(t *testing.T) {
	var acc Accumulator
	acc.SkipRecord("mode")
	if err := acc.Add(metaEvent()); err != nil {
		t.Fatal(err)
	}
	s := acc.Session()
	if s.Report.SkippedRecords["mode"] != 1 {
		t.Errorf("SkippedRecords = %v, want mode:1", s.Report.SkippedRecords)
	}
}

func TestSessionDetachesAccumulator(t *testing.T) {
	acc := NewAccumulator()
	if err := acc.Add(metaEvent()); err != nil {
		t.Fatal(err)
	}
	acc.SkipRecord("mode")
	s := acc.Session()

	// Further accumulator use must not mutate the returned record.
	acc.SkipRecord("mode")
	if err := acc.Add(Event{Kind: KindThinking, Thinking: &Thinking{Text: "hm"}}); err != nil {
		t.Fatal(err)
	}
	if len(s.Events) != 1 {
		t.Errorf("len(Events) = %d, want 1", len(s.Events))
	}
	if s.Report.SkippedRecords["mode"] != 1 {
		t.Errorf("SkippedRecords = %v, want mode:1", s.Report.SkippedRecords)
	}
}

func TestAccumulator(t *testing.T) {
	acc := NewAccumulator()
	events := []Event{
		metaEvent(),
		{Kind: KindUserMessage, UserMessage: &UserMessage{
			Origin:  OriginHuman,
			Content: []ContentBlock{{Kind: ContentText, Text: "hello"}},
		}},
		{Kind: KindToolCall, MessageID: "m1", ToolCall: &ToolCall{
			ToolCallID: "t1", Name: "Read", Kind: ToolKindRead,
		}},
		{Kind: KindAssistantMessage, MessageID: "m1", AssistantMessage: &AssistantMessage{
			Model: "claude-fable-5", StopReason: "tool_use",
			Usage: &TokenUsage{InputTokens: 100, OutputTokens: 10},
		}},
		{Kind: KindToolResult, ToolResult: &ToolResult{
			ToolCallID: "t1", ToolName: "Read",
			Content: []ContentBlock{{Kind: ContentText, Text: "file body"}},
		}},
		{Kind: KindToolResult, ToolResult: &ToolResult{
			ToolCallID: "t-orphan",
		}},
		{Kind: KindAssistantMessage, MessageID: "m2", AssistantMessage: &AssistantMessage{
			Model: "claude-fable-5", StopReason: "end_turn",
			Content: []ContentBlock{{Kind: ContentText, Text: "done"}},
			Usage:   &TokenUsage{InputTokens: 200, OutputTokens: 20},
		}},
	}
	for i, ev := range events {
		if err := acc.Add(ev); err != nil {
			t.Fatalf("Add(%d): %v", i, err)
		}
	}
	acc.SkipRecord("ai-title")
	acc.SkipRecord("ai-title")
	acc.SkipRecord("mode")

	s := acc.Session()

	if s.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", s.SchemaVersion, SchemaVersion)
	}
	if s.Meta.SessionID != "s-1" {
		t.Errorf("Meta.SessionID = %q, want s-1", s.Meta.SessionID)
	}
	if len(s.Events) != len(events) {
		t.Errorf("len(Events) = %d, want %d", len(s.Events), len(events))
	}
	if s.Totals == nil || s.Totals.InputTokens != 300 || s.Totals.OutputTokens != 30 {
		t.Errorf("Totals = %+v, want input 300 / output 30", s.Totals)
	}
	if s.Report.SkippedRecords["ai-title"] != 2 || s.Report.SkippedRecords["mode"] != 1 {
		t.Errorf("SkippedRecords = %v", s.Report.SkippedRecords)
	}
	if s.Report.OrphanToolResults != 1 {
		t.Errorf("OrphanToolResults = %d, want 1", s.Report.OrphanToolResults)
	}
}

func TestAccumulatorRejectsDuplicateMeta(t *testing.T) {
	acc := NewAccumulator()
	if err := acc.Add(metaEvent()); err != nil {
		t.Fatalf("first meta: %v", err)
	}
	if err := acc.Add(metaEvent()); err == nil {
		t.Error("second meta: want error")
	}
}

func TestToolInteractions(t *testing.T) {
	acc := NewAccumulator()
	add := func(ev Event) {
		t.Helper()
		if err := acc.Add(ev); err != nil {
			t.Fatal(err)
		}
	}
	add(metaEvent())
	add(Event{Kind: KindToolCall, ToolCall: &ToolCall{ToolCallID: "t1", Name: "Bash", Kind: ToolKindExecute}})
	add(Event{Kind: KindToolCall, ToolCall: &ToolCall{ToolCallID: "t2", Name: "Read", Kind: ToolKindRead}})
	add(Event{Kind: KindToolResult, ToolResult: &ToolResult{ToolCallID: "t2", ToolName: "Read"}})
	add(Event{Kind: KindToolResult, ToolResult: &ToolResult{ToolCallID: "t-orphan"}})
	s := acc.Session()

	ti := s.ToolInteractions()
	if len(ti) != 3 {
		t.Fatalf("len = %d, want 3 (two calls + one orphan)", len(ti))
	}
	if ti[0].Call.ToolCall.ToolCallID != "t1" || len(ti[0].Results) != 0 {
		t.Errorf("ti[0]: want unanswered call t1, got %+v", ti[0])
	}
	if ti[1].Call.ToolCall.ToolCallID != "t2" || len(ti[1].Results) != 1 {
		t.Errorf("ti[1]: want answered call t2, got %+v", ti[1])
	}
	if ti[2].Call != nil || len(ti[2].Results) != 1 {
		t.Errorf("ti[2]: want orphan result, got %+v", ti[2])
	}
}

func TestEventJSONRoundTrip(t *testing.T) {
	ev := Event{
		Kind:      KindToolResult,
		MessageID: "m1",
		ToolResult: &ToolResult{
			ToolCallID: "t1",
			ToolName:   "WebFetch",
			Content:    []ContentBlock{{Kind: ContentText, Text: "summary of page"}},
			Fetch:      &FetchInfo{URL: "http://localhost/doc", RawBytes: 52000, DurationMS: 812},
			Enrichment: json.RawMessage(`{"bytes":52000}`),
		},
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var got Event
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if err := got.Validate(); err != nil {
		t.Errorf("round-tripped event invalid: %v", err)
	}
	if got.ToolResult.Fetch.RawBytes != 52000 {
		t.Errorf("Fetch.RawBytes = %d, want 52000", got.ToolResult.Fetch.RawBytes)
	}
	if got.ToolResult.Text() != "summary of page" {
		t.Errorf("Text() = %q", got.ToolResult.Text())
	}
}
