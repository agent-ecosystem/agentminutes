package session

import (
	"testing"
	"time"
)

func ts(sec int) *time.Time {
	t := time.Date(2026, 7, 19, 10, 0, sec, 0, time.UTC)
	return &t
}

func TestStats(t *testing.T) {
	acc := NewAccumulator()
	add := func(ev Event) {
		t.Helper()
		if err := acc.Add(ev); err != nil {
			t.Fatal(err)
		}
	}

	add(Event{Kind: KindSessionMeta, Timestamp: ts(0), SessionMeta: &Meta{Harness: "claude-code", SessionID: "s"}})
	add(Event{Kind: KindUserMessage, Timestamp: ts(1), UserMessage: &UserMessage{
		Origin: OriginHuman, Content: []ContentBlock{{Kind: ContentText, Text: "question"}},
	}})
	add(Event{Kind: KindUserMessage, Timestamp: ts(1), UserMessage: &UserMessage{
		Origin: OriginHarness, Content: []ContentBlock{{Kind: ContentText, Text: "reminder"}},
	}})
	// Read: answered, 10 bytes, 2s latency.
	add(Event{Kind: KindToolCall, Timestamp: ts(2), ToolCall: &ToolCall{ToolCallID: "t1", Name: "Read", Kind: ToolKindRead}})
	add(Event{Kind: KindToolResult, Timestamp: ts(4), ToolResult: &ToolResult{
		ToolCallID: "t1", ToolName: "Read",
		Content: []ContentBlock{{Kind: ContentText, Text: "0123456789"}},
	}})
	// WebFetch: answered with error, truncated content (original 5000B),
	// raw fetch 52000B, 3s latency.
	add(Event{Kind: KindToolCall, Timestamp: ts(5), ToolCall: &ToolCall{ToolCallID: "t2", Name: "WebFetch", Kind: ToolKindFetch}})
	add(Event{Kind: KindToolResult, Timestamp: ts(8), ToolResult: &ToolResult{
		ToolCallID: "t2", ToolName: "WebFetch", IsError: true,
		Content: []ContentBlock{{Kind: ContentText, Truncated: true, Size: 5000, Digest: "abc"}},
		Fetch:   &FetchInfo{URL: "http://x", RawBytes: 52000},
	}})
	// Grep: unanswered.
	add(Event{Kind: KindToolCall, Timestamp: ts(9), ToolCall: &ToolCall{ToolCallID: "t3", Name: "Grep", Kind: ToolKindSearch}})
	// Orphan result, 4 bytes.
	add(Event{Kind: KindToolResult, Timestamp: ts(10), ToolResult: &ToolResult{
		ToolCallID: "ghost", Content: []ContentBlock{{Kind: ContentText, Text: "late"}},
	}})
	// Telemetry-only action (no tool_call counterpart).
	add(Event{Kind: KindSystem, Timestamp: ts(10), System: &SystemEvent{Subtype: "web_search_end"}})
	// Two assistant messages; the last non-empty is the final answer, and
	// the mid-session model switch must surface as two Models entries.
	add(Event{Kind: KindAssistantMessage, Timestamp: ts(3), AssistantMessage: &AssistantMessage{
		Model:   "model-a",
		Content: []ContentBlock{{Kind: ContentText, Text: "intermediate"}},
		Usage:   &TokenUsage{InputTokens: 100, OutputTokens: 10},
	}})
	add(Event{Kind: KindAssistantMessage, Timestamp: ts(11), AssistantMessage: &AssistantMessage{
		Model:   "model-b",
		Content: []ContentBlock{{Kind: ContentText, Text: "the final answer"}},
		Usage:   &TokenUsage{InputTokens: 200, OutputTokens: 20},
	}})

	st := acc.Session().Stats()

	if st.Events != 12 {
		t.Errorf("Events = %d, want 12", st.Events)
	}
	if st.SystemBySubtype["web_search_end"] != 1 {
		t.Errorf("SystemBySubtype = %v", st.SystemBySubtype)
	}
	if st.EventCounts[KindToolCall] != 3 || st.EventCounts[KindToolResult] != 3 {
		t.Errorf("EventCounts = %v", st.EventCounts)
	}
	if st.UserMessages != 1 || st.HarnessMessages != 1 {
		t.Errorf("user/harness = %d/%d, want 1/1", st.UserMessages, st.HarnessMessages)
	}
	if st.ToolCalls != 3 {
		t.Errorf("ToolCalls = %d, want 3", st.ToolCalls)
	}
	if st.ToolCallsByName["Read"] != 1 || st.ToolCallsByName["WebFetch"] != 1 || st.ToolCallsByName["Grep"] != 1 {
		t.Errorf("ToolCallsByName = %v", st.ToolCallsByName)
	}
	if st.ToolCallsByKind[ToolKindFetch] != 1 || st.ToolCallsByKind[ToolKindRead] != 1 {
		t.Errorf("ToolCallsByKind = %v", st.ToolCallsByKind)
	}
	if st.ToolErrors != 1 || st.UnansweredCalls != 1 || st.OrphanResults != 1 {
		t.Errorf("errors/unanswered/orphans = %d/%d/%d, want 1/1/1",
			st.ToolErrors, st.UnansweredCalls, st.OrphanResults)
	}
	// 10 (Read) + 5000 (truncated original size) + 4 (orphan).
	if st.ResultBytes != 5014 {
		t.Errorf("ResultBytes = %d, want 5014", st.ResultBytes)
	}
	if st.ResultBytesByName["Read"] != 10 || st.ResultBytesByName["WebFetch"] != 5000 {
		t.Errorf("ResultBytesByName = %v", st.ResultBytesByName)
	}
	if st.ResultBytesByName[UnknownToolName] != 4 {
		t.Errorf("orphan bytes = %v", st.ResultBytesByName)
	}
	if st.FetchRawBytes != 52000 {
		t.Errorf("FetchRawBytes = %d, want 52000", st.FetchRawBytes)
	}
	if st.ToolTimeMSByName["Read"] != 2000 || st.ToolTimeMSByName["WebFetch"] != 3000 {
		t.Errorf("ToolTimeMSByName = %v", st.ToolTimeMSByName)
	}
	if st.Totals == nil || st.Totals.InputTokens != 300 || st.Totals.OutputTokens != 30 {
		t.Errorf("Totals = %+v", st.Totals)
	}
	if len(st.Models) != 2 || st.Models[0] != "model-a" || st.Models[1] != "model-b" {
		t.Errorf("Models = %v, want [model-a model-b] in first-observed order", st.Models)
	}
	if st.StartTime == nil || st.StartTime.Second() != 0 || st.EndTime.Second() != 11 {
		t.Errorf("Start/End = %v/%v", st.StartTime, st.EndTime)
	}
	if st.WallTimeMS != 11000 {
		t.Errorf("WallTimeMS = %d, want 11000", st.WallTimeMS)
	}
	if st.FinalAnswer != "the final answer" {
		t.Errorf("FinalAnswer = %q", st.FinalAnswer)
	}
}

func TestStatsEmptySession(t *testing.T) {
	st := NewAccumulator().Session().Stats()
	if st.Events != 0 || st.ToolCalls != 0 || st.WallTimeMS != 0 || st.FinalAnswer != "" {
		t.Errorf("empty stats = %+v", st)
	}
	if st.StartTime != nil || st.EndTime != nil {
		t.Errorf("empty session should have no timestamps")
	}
	if st.Totals != nil {
		t.Errorf("Totals = %+v, want nil when no usage was recorded", st.Totals)
	}
}
