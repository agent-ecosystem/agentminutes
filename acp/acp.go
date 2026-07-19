// Package acp projects normalized sessions onto the Agent Client
// Protocol's session/update notification vocabulary.
//
// Project answers, empirically and per transcript, what an ACP adapter
// would have reported about a session versus what the native transcript
// records: the returned updates are the notification stream, and the
// LossReport itemizes everything that did not survive the projection.
// agentminutes parses transcripts post-hoc precisely because driving
// harnesses through ACP adapters could perturb the behavior being
// measured; this package quantifies what that decision preserves.
//
// The inverse direction, parsing a recorded ACP session stream as a peer
// harness adapter, is planned but not yet implemented.
//
// Bookkeeping fields (event IDs, message IDs, provenance) are transcript
// artifacts with no ACP meaning and are not counted as losses.
package acp

import (
	"encoding/json"

	"github.com/agent-ecosystem/agentminutes/session"
)

// Session-update discriminator values, per ACP.
const (
	UserMessageChunk  = "user_message_chunk"
	AgentMessageChunk = "agent_message_chunk"
	AgentThoughtChunk = "agent_thought_chunk"

	// ToolCallStart is the initial tool_call update; ToolCallUpdate is the
	// follow-up (here always terminal) tool_call_update.
	ToolCallStart  = "tool_call"
	ToolCallUpdate = "tool_call_update"
)

// Tool-call status values, per ACP. The projection emits pending on the
// initial tool_call, and completed or failed on the terminal
// tool_call_update.
const (
	StatusPending   = "pending"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

// Update is one ACP session/update notification payload.
type Update interface{ update() }

// ContentBlock is ACP's content block wire shape (text only: non-text
// blocks in normalized sessions hold harness-native raw payloads that have
// no faithful ACP encoding, and are reported as losses instead).
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// MessageChunk is a user_message_chunk, agent_message_chunk, or
// agent_thought_chunk update.
type MessageChunk struct {
	SessionUpdate string       `json:"sessionUpdate"`
	Content       ContentBlock `json:"content"`
}

func (MessageChunk) update() {}

// ToolCall is the initial tool_call update.
type ToolCall struct {
	SessionUpdate string          `json:"sessionUpdate"`
	ToolCallID    string          `json:"toolCallId"`
	Title         string          `json:"title,omitempty"`
	Kind          string          `json:"kind,omitempty"`
	Status        string          `json:"status,omitempty"`
	RawInput      json.RawMessage `json:"rawInput,omitempty"`
}

func (ToolCall) update() {}

// ToolCallContent is one content entry of a tool_call_update.
type ToolCallContent struct {
	Type    string       `json:"type"`
	Content ContentBlock `json:"content"`
}

// ToolResult is the terminal tool_call_update carrying the outcome.
type ToolResult struct {
	SessionUpdate string            `json:"sessionUpdate"`
	ToolCallID    string            `json:"toolCallId"`
	Status        string            `json:"status,omitempty"`
	Content       []ToolCallContent `json:"content,omitempty"`

	// RawOutput carries the normalized event's harness Enrichment (the
	// closest transcript analog of a live adapter's raw tool output); a
	// consumer comparing against a real ACP stream should expect this
	// divergence.
	RawOutput json.RawMessage `json:"rawOutput,omitempty"`
}

func (ToolResult) update() {}

// LossReport itemizes what a session's ACP projection could not carry.
type LossReport struct {
	// TotalEvents is the number of normalized events considered.
	TotalEvents int `json:"total_events"`

	// ProjectedEvents counts events that produced at least one update.
	ProjectedEvents int `json:"projected_events"`

	// Updates is the number of session updates emitted.
	Updates int `json:"updates"`

	// DroppedEvents counts events with no ACP representation, keyed by
	// reason (e.g. "session_meta", "system/turn_duration",
	// "thinking_empty" for encrypted reasoning with no plaintext).
	DroppedEvents map[string]int `json:"dropped_events,omitempty"`

	// DroppedFields counts fields that were lost from events that did
	// project (e.g. "timestamp", "assistant_message.usage",
	// "content_block:image").
	DroppedFields map[string]int `json:"dropped_fields,omitempty"`
}

// Project renders a session as the ACP session/update stream an ACP
// adapter would have emitted, in event order, with a report of everything
// that did not survive.
func Project(s *session.Session) ([]Update, *LossReport) {
	p := &projector{report: &LossReport{
		DroppedEvents: make(map[string]int),
		DroppedFields: make(map[string]int),
	}}
	for i := range s.Events {
		p.event(&s.Events[i])
	}
	p.report.Updates = len(p.updates)
	return p.updates, p.report
}

type projector struct {
	updates []Update
	report  *LossReport
}

func (p *projector) event(ev *session.Event) {
	p.report.TotalEvents++
	// ACP updates carry no timestamps: the protocol is live and time is
	// implicit in delivery. Every recorded timestamp is a loss.
	if ev.Timestamp != nil {
		p.report.DroppedFields["timestamp"]++
	}
	before := len(p.updates)

	switch ev.Kind {
	case session.KindSessionMeta:
		// ACP conveys session identity in session/new, not as an update.
		p.report.DroppedEvents["session_meta"]++

	case session.KindUserMessage:
		if ev.UserMessage.Origin == session.OriginHarness {
			p.report.DroppedFields["user_message.origin"]++
		}
		p.chunks(UserMessageChunk, ev.UserMessage.Content)
		if len(p.updates) == before {
			p.report.DroppedEvents["user_message_empty"]++
		}

	case session.KindAssistantMessage:
		am := ev.AssistantMessage
		if am.Model != "" {
			p.report.DroppedFields["assistant_message.model"]++
		}
		if am.StopReason != "" {
			p.report.DroppedFields["assistant_message.stop_reason"]++
		}
		if am.Usage != nil {
			p.report.DroppedFields["assistant_message.usage"]++
		}
		p.chunks(AgentMessageChunk, am.Content)
		if len(p.updates) == before {
			// Accounting anchors with no text (all content became thinking
			// or tool calls) have nothing to say in ACP.
			p.report.DroppedEvents["assistant_message_empty"]++
		}

	case session.KindThinking:
		if ev.Thinking.Signature != "" {
			p.report.DroppedFields["thinking.signature"]++
		}
		if ev.Thinking.Text != "" {
			p.add(MessageChunk{
				SessionUpdate: AgentThoughtChunk,
				Content:       textContent(ev.Thinking.Text),
			})
		} else {
			// Encrypted reasoning with no plaintext: invisible to ACP.
			p.report.DroppedEvents["thinking_empty"]++
		}

	case session.KindToolCall:
		tc := ev.ToolCall
		p.add(ToolCall{
			SessionUpdate: ToolCallStart,
			ToolCallID:    tc.ToolCallID,
			Title:         tc.Name,
			Kind:          string(tc.Kind),
			Status:        StatusPending,
			RawInput:      tc.Input,
		})

	case session.KindToolResult:
		tr := ev.ToolResult
		if tr.Fetch != nil {
			p.report.DroppedFields["tool_result.fetch"]++
		}
		status := StatusCompleted
		if tr.IsError {
			status = StatusFailed
		}
		var content []ToolCallContent
		for i := range tr.Content {
			b := &tr.Content[i]
			if !p.textBlock(b) {
				continue
			}
			content = append(content, ToolCallContent{
				Type:    "content",
				Content: textContent(b.Text),
			})
		}
		p.add(ToolResult{
			SessionUpdate: ToolCallUpdate,
			ToolCallID:    tr.ToolCallID,
			Status:        status,
			Content:       content,
			RawOutput:     tr.Enrichment,
		})

	case session.KindSystem:
		p.report.DroppedEvents["system/"+ev.System.Subtype]++

	case session.KindUnknown:
		p.report.DroppedEvents["unknown"]++
	}

	if len(p.updates) > before {
		p.report.ProjectedEvents++
	}
}

// chunks emits one message chunk per projectable content block.
func (p *projector) chunks(kind string, blocks []session.ContentBlock) {
	for i := range blocks {
		b := &blocks[i]
		if !p.textBlock(b) {
			continue
		}
		p.add(MessageChunk{
			SessionUpdate: kind,
			Content:       textContent(b.Text),
		})
	}
}

// textBlock reports whether a block projects as text, recording the loss
// when it cannot.
func (p *projector) textBlock(b *session.ContentBlock) bool {
	switch {
	case b.Truncated:
		p.report.DroppedFields["content_block:truncated"]++
		return false
	case b.Kind == session.ContentText && b.Text != "":
		return true
	case b.Kind == session.ContentText:
		return false // empty text: nothing to project, nothing lost
	default:
		p.report.DroppedFields["content_block:"+string(b.Kind)]++
		return false
	}
}

func (p *projector) add(u Update) { p.updates = append(p.updates, u) }

// textContent builds ACP's text content block.
func textContent(text string) ContentBlock {
	return ContentBlock{Type: "text", Text: text}
}
