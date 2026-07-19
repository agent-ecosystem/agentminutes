package session

import "encoding/json"

// Origin distinguishes genuine human input from context the harness
// injected into the user role (reminders, attachments, tool listings).
type Origin string

// Origin values.
const (
	OriginHuman   Origin = "human"
	OriginHarness Origin = "harness"
)

// UserMessage is content presented to the model as user input.
type UserMessage struct {
	// Origin marks who authored the message. Harness-injected records
	// (attachments, meta prompts) must not be counted as user intent.
	Origin  Origin         `json:"origin" schema:"ext"`
	Content []ContentBlock `json:"content" schema:"acp"`
}

// Text returns the concatenated text content of the message.
func (m *UserMessage) Text() string { return blocksText(m.Content) }

// AssistantMessage is one complete assistant API message, folded from
// however many transcript records the harness split it across. Exactly one
// is emitted per API message even when all content became separate thinking
// or tool_call events, so usage totals can always be derived from these.
type AssistantMessage struct {
	// Model that produced the message (gen_ai.response.model).
	Model string `json:"model,omitempty" schema:"otel"`

	// Content holds the message's text blocks. Thinking and tool_use
	// blocks are emitted as their own events sharing this event's
	// MessageID.
	Content []ContentBlock `json:"content,omitempty" schema:"acp"`

	// StopReason is the API's finish reason
	// (gen_ai.response.finish_reasons), e.g. "end_turn", "tool_use".
	StopReason string `json:"stop_reason,omitempty" schema:"otel"`

	// Usage is the final token accounting for the API message. When a
	// harness writes usage as a growing snapshot across split records, the
	// adapter must take the last snapshot, never a sum.
	Usage *TokenUsage `json:"usage,omitempty" schema:"otel"`
}

// Text returns the concatenated text content of the message.
func (m *AssistantMessage) Text() string { return blocksText(m.Content) }

// Thinking is an extended-thinking block from an assistant message.
type Thinking struct {
	Text string `json:"text" schema:"acp"`

	// Signature is the provider's opaque integrity signature, when present.
	Signature string `json:"signature,omitempty" schema:"ext"`
}

// ToolKind classifies a tool call using ACP's tool kind vocabulary.
type ToolKind string

// ToolKind values, per ACP.
const (
	ToolKindRead       ToolKind = "read"
	ToolKindEdit       ToolKind = "edit"
	ToolKindDelete     ToolKind = "delete"
	ToolKindMove       ToolKind = "move"
	ToolKindSearch     ToolKind = "search"
	ToolKindExecute    ToolKind = "execute"
	ToolKindThink      ToolKind = "think"
	ToolKindFetch      ToolKind = "fetch"
	ToolKindSwitchMode ToolKind = "switch_mode"
	ToolKindOther      ToolKind = "other"
)

// ToolCall is a tool invocation request.
type ToolCall struct {
	// ToolCallID correlates this call with its ToolResult.
	ToolCallID string `json:"tool_call_id" schema:"acp"`

	// Name is the harness-native tool name (e.g. "Read", "WebFetch").
	Name string `json:"name" schema:"ext"`

	// Kind is the ACP classification of the tool (adapters map native
	// names onto it; unmappable names get ToolKindOther).
	Kind ToolKind `json:"kind" schema:"acp"`

	// Input is the full tool input as recorded (ACP rawInput).
	Input json.RawMessage `json:"input,omitempty" schema:"acp"`

	// PromotedFrom marks a call synthesized by an opt-in telemetry
	// promotion (a session.Transform), naming the native source (e.g.
	// "event_msg/web_search_end"). Promoted events may double-count
	// actions a harness version also records natively; this marker is
	// what lets consumers audit or dedupe. Empty for natively recorded
	// calls.
	PromotedFrom string `json:"promoted_from,omitempty" schema:"ext"`
}

// ToolResult is the outcome of a tool call.
type ToolResult struct {
	// ToolCallID correlates this result with its ToolCall. Results whose
	// ID matches no observed call are orphans; they are preserved and
	// counted in the session report.
	ToolCallID string `json:"tool_call_id" schema:"acp"`

	// ToolName is the name of the called tool, denormalized for
	// convenience. Empty for orphaned results.
	ToolName string `json:"tool_name,omitempty" schema:"ext"`

	// IsError marks a failed call (ACP status "failed").
	IsError bool `json:"is_error,omitempty" schema:"acp"`

	// Content is what the model actually received back. For summarizing
	// pipelines (e.g. Claude Code WebFetch) this is the post-pipeline
	// content, deliberately distinct from Fetch.RawBytes.
	Content []ContentBlock `json:"content,omitempty" schema:"acp"`

	// Fetch carries retrieval metrics when the harness recorded them for
	// a fetch-kind tool.
	Fetch *FetchInfo `json:"fetch,omitempty" schema:"ext"`

	// Enrichment is the harness's structured sidecar data for this result
	// (e.g. Claude Code's toolUseResult), verbatim.
	Enrichment json.RawMessage `json:"enrichment,omitempty" schema:"ext"`

	// PromotedFrom marks a result synthesized by an opt-in telemetry
	// promotion; see ToolCall.PromotedFrom.
	PromotedFrom string `json:"promoted_from,omitempty" schema:"ext"`
}

// Text returns the concatenated text content of the result.
func (r *ToolResult) Text() string { return blocksText(r.Content) }

// FetchInfo holds retrieval metrics for fetch-kind tool results.
type FetchInfo struct {
	URL string `json:"url,omitempty" schema:"ext"`

	// RawBytes is the size of the fetched resource before any conversion
	// or summarization pipeline. Compare with the result Content size to
	// measure pipeline compression.
	RawBytes   int64 `json:"raw_bytes,omitempty" schema:"ext"`
	StatusCode int   `json:"status_code,omitempty" schema:"ext"`
	DurationMS int64 `json:"duration_ms,omitempty" schema:"ext"`
}

// SystemEvent is harness- or API-level activity: injected context, turn
// diagnostics, API errors and retries.
type SystemEvent struct {
	// Subtype is the harness-native classification, namespaced by the
	// adapter (e.g. "turn_duration", "attachment/task_reminder").
	Subtype string `json:"subtype" schema:"ext"`

	// Level is the harness-native severity, when present (e.g. "error").
	Level string `json:"level,omitempty" schema:"ext"`

	// Text is the human-readable content, when present.
	Text string `json:"text,omitempty" schema:"ext"`

	// Details is the record's structured payload, verbatim.
	Details json.RawMessage `json:"details,omitempty" schema:"ext"`
}

// Unknown wraps a record the adapter could not classify, preserved verbatim.
// Emitted only in permissive mode.
type Unknown struct {
	// RecordType is the native discriminator value, when one exists.
	RecordType string `json:"record_type,omitempty" schema:"ext"`

	// Reason says why classification failed.
	Reason string `json:"reason" schema:"ext"`

	// Raw is the verbatim source record.
	Raw json.RawMessage `json:"raw" schema:"ext"`
}
