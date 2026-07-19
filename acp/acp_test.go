package acp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/agent-ecosystem/agentminutes/session"
)

func buildSession(t *testing.T) *session.Session {
	t.Helper()
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	acc := session.NewAccumulator()
	add := func(ev session.Event) {
		t.Helper()
		if err := acc.Add(ev); err != nil {
			t.Fatal(err)
		}
	}

	add(session.Event{Kind: session.KindSessionMeta, Timestamp: &now, SessionMeta: &session.Meta{
		Harness: "claude-code", SessionID: "s-1",
	}})
	add(session.Event{Kind: session.KindUserMessage, UserMessage: &session.UserMessage{
		Origin: session.OriginHuman,
		Content: []session.ContentBlock{
			{Kind: session.ContentText, Text: "what does the doc say?"},
			{Kind: session.ContentImage, Raw: json.RawMessage(`{"type":"image"}`)},
		},
	}})
	add(session.Event{Kind: session.KindUserMessage, UserMessage: &session.UserMessage{
		Origin:  session.OriginHarness,
		Content: []session.ContentBlock{{Kind: session.ContentText, Text: "<reminder/>"}},
	}})
	add(session.Event{Kind: session.KindThinking, Thinking: &session.Thinking{
		Text: "let me check", Signature: "sig1",
	}})
	add(session.Event{Kind: session.KindThinking, Thinking: &session.Thinking{
		Signature: "ENCRYPTED", // encrypted reasoning: no plaintext
	}})
	add(session.Event{Kind: session.KindToolCall, ToolCall: &session.ToolCall{
		ToolCallID: "t1", Name: "Read", Kind: session.ToolKindRead,
		Input: json.RawMessage(`{"file_path":"/doc.md"}`),
	}})
	add(session.Event{Kind: session.KindToolResult, ToolResult: &session.ToolResult{
		ToolCallID: "t1", ToolName: "Read",
		Content: []session.ContentBlock{
			{Kind: session.ContentText, Text: "doc body"},
			{Kind: session.ContentText, Truncated: true, Size: 9999, Digest: "d"},
		},
		Fetch:      &session.FetchInfo{URL: "http://x", RawBytes: 100},
		Enrichment: json.RawMessage(`{"bytes":100}`),
	}})
	add(session.Event{Kind: session.KindToolResult, ToolResult: &session.ToolResult{
		ToolCallID: "t2", IsError: true,
	}})
	// Empty anchor: all content became the tool call above.
	add(session.Event{Kind: session.KindAssistantMessage, AssistantMessage: &session.AssistantMessage{
		Model: "claude-fable-5", StopReason: "tool_use",
		Usage: &session.TokenUsage{InputTokens: 10, OutputTokens: 1},
	}})
	add(session.Event{Kind: session.KindAssistantMessage, AssistantMessage: &session.AssistantMessage{
		Model:      "claude-fable-5",
		StopReason: "end_turn",
		Content:    []session.ContentBlock{{Kind: session.ContentText, Text: "the answer"}},
		Usage:      &session.TokenUsage{InputTokens: 20, OutputTokens: 5},
	}})
	add(session.Event{Kind: session.KindSystem, System: &session.SystemEvent{Subtype: "turn_duration"}})

	return acc.Session()
}

func TestProject(t *testing.T) {
	updates, loss := Project(buildSession(t))

	wantKinds := []string{
		UserMessageChunk,  // human question
		UserMessageChunk,  // harness reminder (origin lost)
		AgentThoughtChunk, // plaintext thinking
		ToolCallStart,     // Read call
		ToolCallUpdate,    // Read result
		ToolCallUpdate,    // failed t2 result
		AgentMessageChunk, // "the answer"
	}
	if len(updates) != len(wantKinds) {
		t.Fatalf("got %d updates, want %d: %+v", len(updates), len(wantKinds), updates)
	}
	for i, u := range updates {
		var kind string
		switch v := u.(type) {
		case MessageChunk:
			kind = v.SessionUpdate
		case ToolCall:
			kind = v.SessionUpdate
		case ToolResult:
			kind = v.SessionUpdate
		}
		if kind != wantKinds[i] {
			t.Errorf("update %d = %q, want %q", i, kind, wantKinds[i])
		}
	}

	call := updates[3].(ToolCall)
	if call.ToolCallID != "t1" || call.Title != "Read" || call.Kind != "read" || call.Status != StatusPending {
		t.Errorf("tool_call = %+v", call)
	}
	result := updates[4].(ToolResult)
	if result.Status != StatusCompleted || len(result.Content) != 1 || result.Content[0].Content.Text != "doc body" {
		t.Errorf("tool_call_update = %+v", result)
	}
	if len(result.RawOutput) == 0 {
		t.Error("enrichment should ride in rawOutput")
	}
	if failed := updates[5].(ToolResult); failed.Status != StatusFailed {
		t.Errorf("failed result status = %q", failed.Status)
	}

	if loss.TotalEvents != 11 || loss.Updates != 7 || loss.ProjectedEvents != 7 {
		t.Errorf("totals = %d/%d/%d, want 11/7/7", loss.TotalEvents, loss.ProjectedEvents, loss.Updates)
	}
	wantDroppedEvents := map[string]int{
		"session_meta":            1,
		"thinking_empty":          1,
		"assistant_message_empty": 1,
		"system/turn_duration":    1,
	}
	for k, n := range wantDroppedEvents {
		if loss.DroppedEvents[k] != n {
			t.Errorf("DroppedEvents[%s] = %d, want %d", k, loss.DroppedEvents[k], n)
		}
	}
	wantDroppedFields := map[string]int{
		"timestamp":                     1, // only the meta event had one
		"user_message.origin":           1,
		"thinking.signature":            2,
		"assistant_message.model":       2,
		"assistant_message.stop_reason": 2,
		"assistant_message.usage":       2,
		"tool_result.fetch":             1,
		"content_block:image":           1,
		"content_block:truncated":       1,
	}
	for k, n := range wantDroppedFields {
		if loss.DroppedFields[k] != n {
			t.Errorf("DroppedFields[%s] = %d, want %d", k, loss.DroppedFields[k], n)
		}
	}
}

func TestProjectWireFormat(t *testing.T) {
	updates, _ := Project(buildSession(t))
	data, err := json.Marshal(updates)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		`"sessionUpdate":"user_message_chunk"`,
		`"sessionUpdate":"tool_call"`,
		`"toolCallId":"t1"`,
		`"rawInput":{"file_path":"/doc.md"}`,
		`"status":"pending"`,
		`"content":{"type":"text","text":"the answer"}`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("wire JSON missing %s", want)
		}
	}
	if strings.Contains(s, "provenance") || strings.Contains(s, "message_id") {
		t.Error("wire JSON leaked transcript bookkeeping")
	}
}

func TestProjectEmptySession(t *testing.T) {
	updates, loss := Project(session.NewAccumulator().Session())
	if len(updates) != 0 || loss.TotalEvents != 0 || loss.Updates != 0 {
		t.Errorf("empty projection = %d updates, %+v", len(updates), loss)
	}
}
