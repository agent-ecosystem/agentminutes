package session

import (
	"encoding/json"
	"fmt"
	"time"
)

// EventKind identifies the type of a session event.
type EventKind string

// Event kinds. Kinds with an ACP session/update analog are noted; the rest
// are extensions required for post-hoc transcript analysis.
const (
	// KindSessionMeta is the first event of every stream, carrying session
	// identity known at parse start. Extension (ACP conveys this in
	// session/new, not as an update).
	KindSessionMeta EventKind = "session_meta"

	// KindUserMessage is a message presented to the model as user input.
	// ACP analog: user_message_chunk (aggregated, not chunked).
	KindUserMessage EventKind = "user_message"

	// KindAssistantMessage is one complete assistant API message. It is the
	// accounting anchor: exactly one per API message, carrying model, stop
	// reason, and token usage, even when the message has no text content.
	// ACP analog: agent_message_chunk (aggregated, not chunked).
	KindAssistantMessage EventKind = "assistant_message"

	// KindThinking is an extended-thinking block.
	// ACP analog: agent_thought_chunk (aggregated, not chunked).
	KindThinking EventKind = "thinking"

	// KindToolCall is a tool invocation request with its full input.
	// ACP analog: tool_call.
	KindToolCall EventKind = "tool_call"

	// KindToolResult is the outcome of a tool call, correlated by tool call
	// ID. ACP analog: the terminal tool_call_update.
	KindToolResult EventKind = "tool_result"

	// KindSystem is harness- or API-level activity visible in the
	// transcript: injected context, turn diagnostics, API errors and
	// retries. Extension.
	KindSystem EventKind = "system"

	// KindUnknown wraps a record the adapter could not classify. Emitted
	// only in permissive mode; the default is a parse error. Extension.
	KindUnknown EventKind = "unknown"
)

// Event is one ordered entry in a normalized session. Exactly one payload
// field is set, matching Kind. Events sharing a MessageID originate from the
// same assistant API message (e.g. thinking, text, and tool calls emitted
// together).
type Event struct {
	Kind EventKind `json:"kind" schema:"acp"`

	// Timestamp is when the harness recorded the event. ACP updates carry
	// no timestamps, so this is absent when a session is reconstructed
	// from an ACP stream.
	Timestamp *time.Time `json:"timestamp,omitempty" schema:"ext"`

	// ID is the harness-native identifier of the source record.
	ID string `json:"id,omitempty" schema:"ext"`

	// ParentID threads events into the harness's own parent chain.
	ParentID string `json:"parent_id,omitempty" schema:"ext"`

	// MessageID correlates events folded out of one assistant API message.
	MessageID string `json:"message_id,omitempty" schema:"ext"`

	// Provenance points back at the native transcript records this event
	// was derived from.
	Provenance *Provenance `json:"provenance,omitempty" schema:"ext"`

	SessionMeta      *Meta             `json:"session_meta,omitempty" schema:"ext"`
	UserMessage      *UserMessage      `json:"user_message,omitempty" schema:"acp"`
	AssistantMessage *AssistantMessage `json:"assistant_message,omitempty" schema:"acp"`
	Thinking         *Thinking         `json:"thinking,omitempty" schema:"acp"`
	ToolCall         *ToolCall         `json:"tool_call,omitempty" schema:"acp"`
	ToolResult       *ToolResult       `json:"tool_result,omitempty" schema:"acp"`
	System           *SystemEvent      `json:"system,omitempty" schema:"ext"`
	Unknown          *Unknown          `json:"unknown,omitempty" schema:"ext"`
}

// eventPayloads is the single in-package enumeration pairing each event
// kind with its payload field. Validate walks it, and a test in
// schema_test.go pins it to Event's struct fields, so a new kind cannot be
// wired up in one place but forgotten in the other.
var eventPayloads = []struct {
	kind EventKind
	set  func(*Event) bool
}{
	{KindSessionMeta, func(e *Event) bool { return e.SessionMeta != nil }},
	{KindUserMessage, func(e *Event) bool { return e.UserMessage != nil }},
	{KindAssistantMessage, func(e *Event) bool { return e.AssistantMessage != nil }},
	{KindThinking, func(e *Event) bool { return e.Thinking != nil }},
	{KindToolCall, func(e *Event) bool { return e.ToolCall != nil }},
	{KindToolResult, func(e *Event) bool { return e.ToolResult != nil }},
	{KindSystem, func(e *Event) bool { return e.System != nil }},
	{KindUnknown, func(e *Event) bool { return e.Unknown != nil }},
}

// Validate reports whether exactly one payload is set and matches Kind, and
// that correlation-critical payload fields are present: tool calls and tool
// results must carry a non-empty ToolCallID, or ToolInteractions and Stats
// would silently merge distinct interactions.
func (e *Event) Validate() error {
	var set []EventKind
	for _, p := range eventPayloads {
		if p.set(e) {
			set = append(set, p.kind)
		}
	}
	if len(set) != 1 {
		return fmt.Errorf("session: event has %d payloads, want exactly 1 (kind %q)", len(set), e.Kind)
	}
	if set[0] != e.Kind {
		return fmt.Errorf("session: event kind %q does not match payload %q", e.Kind, set[0])
	}
	switch e.Kind {
	case KindToolCall:
		if e.ToolCall.ToolCallID == "" {
			return fmt.Errorf("session: %s event has empty tool_call_id", e.Kind)
		}
	case KindToolResult:
		if e.ToolResult.ToolCallID == "" {
			return fmt.Errorf("session: %s event has empty tool_call_id", e.Kind)
		}
	}
	return nil
}

// Provenance locates the native transcript records an event was derived
// from. Line numbers are 1-based. EndLine equals Line for single-record
// events and is greater for folded events (e.g. a split assistant message).
type Provenance struct {
	Line    int `json:"line" schema:"ext"`
	EndLine int `json:"end_line,omitempty" schema:"ext"`

	// Raw holds the verbatim source records when parsing was configured to
	// retain them.
	Raw []json.RawMessage `json:"raw,omitempty" schema:"ext"`
}
