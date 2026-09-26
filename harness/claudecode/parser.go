package claudecode

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"strings"
	"time"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/parseutil"
	"github.com/agent-ecosystem/agentminutes/session"
)

// record is the top-level envelope of one transcript line.
type record struct {
	Type        string          `json:"type"`
	UUID        string          `json:"uuid"`
	ParentUUID  string          `json:"parentUuid"`
	Timestamp   string          `json:"timestamp"`
	SessionID   string          `json:"sessionId"`
	Version     string          `json:"version"`
	CWD         string          `json:"cwd"`
	GitBranch   string          `json:"gitBranch"`
	IsSidechain bool            `json:"isSidechain"`
	AgentID     string          `json:"agentId"`
	IsMeta      bool            `json:"isMeta"`
	Subtype     string          `json:"subtype"`
	Level       string          `json:"level"`
	Content     json.RawMessage `json:"content"`
	Message     json.RawMessage `json:"message"`
	Attachment  json.RawMessage `json:"attachment"`
	// Rendered is the attachment as delivered to the model (2.1.267+):
	// each entry's content is a <system-reminder> block, the injected
	// user-turn text. For attachments whose object holds only structured
	// fields (environment, session_context, auto_mode, date, ...), it is
	// the only record of the text the model saw.
	Rendered []struct {
		Content string `json:"content"`
	} `json:"rendered"`
	// RenderedInHumanTurn (queued_command task notifications, 2.1.267+)
	// is the rendering used when the notification is delivered in the
	// human's turn (right after the user's prompt) rather than mid-turn
	// (after a tool result); recorded only when it differs from Rendered.
	// The harness picks between them by position: its predicate walks
	// back to the nearest conversational record and uses this form when
	// that record is a human prompt (2.1.274 bundle, oNe/eXo). The
	// parser tracks the same state, so which form the model saw is
	// determined, not guessed.
	RenderedInHumanTurn []struct {
		Content string `json:"content"`
	} `json:"renderedInHumanTurn"`
	// IsCompactSummary marks a compaction summary user record, which the
	// harness does not count as a human turn.
	IsCompactSummary bool `json:"isCompactSummary"`
	Origin           *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
	IsAPIError    bool            `json:"isApiErrorMessage"`
}

// apiMessage is the message body of user and assistant records.
type apiMessage struct {
	ID         string          `json:"id"`
	Model      string          `json:"model"`
	StopReason string          `json:"stop_reason"`
	Content    json.RawMessage `json:"content"`
	Usage      json.RawMessage `json:"usage"`
}

// block is one content block of an API message.
type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Signature string          `json:"signature"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`

	raw json.RawMessage
}

// fold accumulates the records of one assistant API message. Claude Code
// splits a message across records (one content block each), and the split
// is not always contiguous: tool results can interleave while later blocks
// of the same message are still streaming. A fold therefore stays open
// across non-assistant records and closes only when an assistant record
// with a different message ID arrives, or at EOF.
type fold struct {
	messageID  string
	uuid       string
	parentUUID string
	firstLine  int
	lastLine   int
	ts         *time.Time
	model      string
	stopReason string
	usage      *session.TokenUsage
	text       []session.ContentBlock
	raw        []json.RawMessage
}

type parser struct {
	parseutil.Emitter
	meta bool
	// toolNames maps tool_use IDs to tool names, for denormalizing results.
	toolNames map[string]string
	fold      *fold
	// closedMIDs guards the fold invariant: a message ID resuming after
	// its fold closed is schema drift and must fail loudly, because the
	// second fold would duplicate the message's accounting anchor.
	closedMIDs map[string]bool
	// inHumanTurn is true while the nearest conversational record so far
	// is a human prompt (not a tool result, a meta record, an assistant
	// message, or a compaction summary); it selects a queued_command
	// task notification's rendering.
	inHumanTurn bool
}

// Events implements harness.Adapter.
func (Adapter) Events(r io.Reader, opts harness.Options) iter.Seq2[session.Event, error] {
	return func(yield func(session.Event, error) bool) {
		p := &parser{
			Emitter:    parseutil.Emitter{Harness: harness.ClaudeCode, Opts: opts, Yield: yield},
			toolNames:  make(map[string]string),
			closedMIDs: make(map[string]bool),
		}
		sc := parseutil.NewLineScanner(r)
		for sc.Scan() {
			p.Line = sc.Line()
			if !p.record(sc.Bytes()) {
				return
			}
		}
		if err := sc.Err(); err != nil {
			p.Line = sc.Line()
			p.Fail("reading transcript", err)
			return
		}
		if p.fold != nil {
			p.closeFold()
		}
	}
}

func (p *parser) record(data []byte) bool {
	var rec record
	if err := json.Unmarshal(data, &rec); err != nil {
		return p.UnknownOrFail("", data, fmt.Sprintf("invalid JSON: %v", err))
	}
	switch {
	case skipTypes[rec.Type]:
		if p.Opts.OnSkip != nil {
			p.Opts.OnSkip(p.Line, rec.Type)
		}
		return true
	case rec.Type == "user":
		return p.conversation(&rec, data, p.user)
	case rec.Type == "assistant":
		return p.conversation(&rec, data, p.assistant)
	case rec.Type == "system":
		return p.conversation(&rec, data, p.system)
	case rec.Type == "attachment":
		return p.conversation(&rec, data, p.attachment)
	case rec.Type == "cost-state":
		return p.conversation(&rec, data, p.costState)
	default:
		return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("unrecognized record type %q", rec.Type))
	}
}

// conversation records session metadata from the first model-visible record
// and emits the session_meta event before any other.
func (p *parser) conversation(rec *record, data []byte, handle func(*record, []byte) bool) bool {
	if p.Version == "" {
		p.Version = rec.Version
	}
	if !p.meta {
		p.meta = true
		ev := session.Event{
			Kind:       session.KindSessionMeta,
			Timestamp:  parseutil.ParseTime(rec.Timestamp),
			Provenance: p.Prov(data),
			SessionMeta: &session.Meta{
				Harness:        string(harness.ClaudeCode),
				HarnessVersion: cmp.Or(rec.Version, p.Opts.HarnessVersionHint),
				SessionID:      rec.SessionID,
				SubagentID:     rec.AgentID,
				IsSubagent:     rec.IsSidechain,
				CWD:            rec.CWD,
				GitBranch:      rec.GitBranch,
			},
		}
		if !p.Emit(ev) {
			return false
		}
	}
	return handle(rec, data)
}

func (p *parser) user(rec *record, data []byte) bool {
	var msg apiMessage
	if len(rec.Message) == 0 {
		return p.UnknownOrFail(rec.Type, data, "user record has no message")
	}
	if err := json.Unmarshal(rec.Message, &msg); err != nil {
		return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("malformed user message: %v", err))
	}
	blocks, err := parseBlocks(msg.Content)
	if err != nil {
		return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("malformed user content: %v", err))
	}
	var content []session.ContentBlock
	emittedResults := false
	for i := range blocks {
		b := &blocks[i]
		if b.Type == "tool_result" {
			if !p.toolResult(rec, b, data) {
				return false
			}
			emittedResults = true
			continue
		}
		content = append(content, toContentBlock(b))
	}
	if emittedResults {
		p.inHumanTurn = false
	}
	if len(content) == 0 && emittedResults {
		// The record's line is accounted for by its tool_result events.
		return true
	}
	// A record with no blocks at all still becomes an (empty) user_message:
	// every line must be an event, a counted skip, or an error.
	origin := session.OriginHuman
	if rec.IsMeta {
		origin = session.OriginHarness
	}
	// A meta record whose only content is a one-line bracketed marker
	// ("[Request interrupted by user]") leaves the turn state alone, as
	// the harness's predicate does; any other meta record, and a
	// compaction summary, ends the human turn.
	if !rec.IsMeta || !isTurnMarker(blocks) {
		p.inHumanTurn = !rec.IsMeta && !rec.IsCompactSummary && (rec.Origin == nil || rec.Origin.Kind == "human")
	}
	return p.Emit(session.Event{
		Kind:        session.KindUserMessage,
		Timestamp:   parseutil.ParseTime(rec.Timestamp),
		ID:          rec.UUID,
		ParentID:    rec.ParentUUID,
		Provenance:  p.Prov(data),
		UserMessage: &session.UserMessage{Origin: origin, Content: content},
	})
}

func (p *parser) toolResult(rec *record, b *block, data []byte) bool {
	content, err := parseBlocks(b.Content)
	if err != nil {
		return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("malformed tool_result content: %v", err))
	}
	blocks := make([]session.ContentBlock, 0, len(content))
	for i := range content {
		blocks = append(blocks, toContentBlock(&content[i]))
	}
	tr := &session.ToolResult{
		ToolCallID: b.ToolUseID,
		ToolName:   p.toolNames[b.ToolUseID],
		IsError:    b.IsError,
		Content:    blocks,
		Fetch:      extractFetch(rec.ToolUseResult),
		Enrichment: rec.ToolUseResult,
	}
	p.Truncate(tr)
	return p.Emit(session.Event{
		Kind:       session.KindToolResult,
		Timestamp:  parseutil.ParseTime(rec.Timestamp),
		ID:         rec.UUID,
		ParentID:   rec.ParentUUID,
		Provenance: p.Prov(data),
		ToolResult: tr,
	})
}

func (p *parser) assistant(rec *record, data []byte) bool {
	p.inHumanTurn = false
	var msg apiMessage
	if len(rec.Message) == 0 {
		return p.UnknownOrFail(rec.Type, data, "assistant record has no message")
	}
	if err := json.Unmarshal(rec.Message, &msg); err != nil {
		return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("malformed assistant message: %v", err))
	}
	blocks, err := parseBlocks(msg.Content)
	if err != nil {
		return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("malformed assistant content: %v", err))
	}

	// API errors are harness-synthesized records (model "<synthetic>"),
	// not real API messages; they surface as system events and never
	// touch the fold.
	if rec.IsAPIError {
		return p.Emit(session.Event{
			Kind:       session.KindSystem,
			Timestamp:  parseutil.ParseTime(rec.Timestamp),
			ID:         rec.UUID,
			ParentID:   rec.ParentUUID,
			Provenance: p.Prov(data),
			System: &session.SystemEvent{
				Subtype: "api_error",
				Level:   "error",
				Text:    blocksPlainText(blocks),
				Details: parseutil.CloneRaw(data),
			},
		})
	}

	if p.fold != nil && p.fold.messageID != msg.ID {
		if !p.closeFold() {
			return false
		}
	}
	if p.fold == nil {
		if p.closedMIDs[msg.ID] {
			return p.Fail(fmt.Sprintf("assistant message %s resumed after its fold closed", msg.ID), nil)
		}
		p.fold = &fold{
			messageID:  msg.ID,
			uuid:       rec.UUID,
			parentUUID: rec.ParentUUID,
			firstLine:  p.Line,
			ts:         parseutil.ParseTime(rec.Timestamp),
			model:      msg.Model,
		}
	}
	f := p.fold
	f.lastLine = p.Line
	if msg.StopReason != "" {
		f.stopReason = msg.StopReason
	}
	// Usage is a growing snapshot across split records: the last one wins.
	u, err := parseUsage(msg.Usage)
	if err != nil {
		return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("malformed usage: %v", err))
	}
	if u != nil {
		f.usage = u
	}
	if p.Opts.KeepRaw {
		f.raw = append(f.raw, parseutil.CloneRaw(data))
	}

	for i := range blocks {
		b := &blocks[i]
		switch b.Type {
		case "thinking":
			ev := session.Event{
				Kind:       session.KindThinking,
				Timestamp:  parseutil.ParseTime(rec.Timestamp),
				ID:         rec.UUID,
				ParentID:   rec.ParentUUID,
				MessageID:  msg.ID,
				Provenance: p.Prov(data),
				Thinking:   &session.Thinking{Text: b.Thinking, Signature: b.Signature},
			}
			if !p.Emit(ev) {
				return false
			}
		case "text":
			f.text = append(f.text, session.ContentBlock{Kind: session.ContentText, Text: b.Text})
		case "tool_use":
			p.toolNames[b.ID] = b.Name
			ev := session.Event{
				Kind:       session.KindToolCall,
				Timestamp:  parseutil.ParseTime(rec.Timestamp),
				ID:         rec.UUID,
				ParentID:   rec.ParentUUID,
				MessageID:  msg.ID,
				Provenance: p.Prov(data),
				ToolCall: &session.ToolCall{
					ToolCallID: b.ID,
					Name:       b.Name,
					Kind:       kindFor(b.Name),
					Input:      b.Input,
				},
			}
			if !p.Emit(ev) {
				return false
			}
		case "fallback":
			// A mid-message model switch (observed 2.1.197): the block
			// carries {from: {model}, to: {model}} and the message's
			// remaining records continue on the new model. Surfaced as a
			// system event so consumers can flag the run (the answering
			// model changed); the subtype is namespaced like attachment
			// subtypes because the source is a content block, not a
			// system record.
			ev := session.Event{
				Kind:       session.KindSystem,
				Timestamp:  parseutil.ParseTime(rec.Timestamp),
				ID:         rec.UUID,
				ParentID:   rec.ParentUUID,
				MessageID:  msg.ID,
				Provenance: p.Prov(data),
				System: &session.SystemEvent{
					Subtype: "assistant/fallback",
					Details: parseutil.CloneRaw(b.raw),
				},
			}
			if !p.Emit(ev) {
				return false
			}
		default:
			return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("unrecognized assistant content block %q", b.Type))
		}
	}
	return true
}

// closeFold emits the message's accounting anchor: exactly one
// assistant_message event per API message, carrying model, stop reason,
// final usage, and the folded text content. Its provenance spans the range
// of the folded records (which can include interleaved records when tool
// results arrived mid-message).
func (p *parser) closeFold() bool {
	f := p.fold
	p.fold = nil
	p.closedMIDs[f.messageID] = true
	return p.Emit(session.Event{
		Kind:       session.KindAssistantMessage,
		Timestamp:  f.ts,
		ID:         f.uuid,
		ParentID:   f.parentUUID,
		MessageID:  f.messageID,
		Provenance: &session.Provenance{Line: f.firstLine, EndLine: f.lastLine, Raw: f.raw},
		AssistantMessage: &session.AssistantMessage{
			Model:      f.model,
			Content:    f.text,
			StopReason: f.stopReason,
			Usage:      f.usage,
		},
	})
}

func (p *parser) system(rec *record, data []byte) bool {
	var text string
	if len(rec.Content) > 0 {
		// content is a string for textual subtypes; ignore other shapes,
		// the full record rides along in Details.
		_ = json.Unmarshal(rec.Content, &text)
	}
	return p.Emit(session.Event{
		Kind:       session.KindSystem,
		Timestamp:  parseutil.ParseTime(rec.Timestamp),
		ID:         rec.UUID,
		ParentID:   rec.ParentUUID,
		Provenance: p.Prov(data),
		System: &session.SystemEvent{
			Subtype: rec.Subtype,
			Level:   rec.Level,
			Text:    text,
			Details: parseutil.CloneRaw(data),
		},
	})
}

// costState preserves the session's cost tracker, which interactive
// sessions persist at orderly shutdown (2.1.267+; a hard kill writes
// nothing): totalCostUSD, API/tool/wall durations, lines added/removed,
// and per-model token usage (with thinkingTokens and webSearchRequests)
// for every API request the session made, subagents included. That is a
// superset of what the transcript records (helper calls such as title
// generation never appear as assistant records), so it is telemetry,
// never accounting: Totals stay derived from message usage, and the record
// rides along verbatim as a system event. One record per process exit: a
// resumed session restores the tracker from the last record in the
// transcript, appends after it, and writes another, cumulative, at its own
// exit, so the session total is the last one (minus any process that died
// without writing, which nothing restores). It is usually but not always
// the final record (the /exit command's echo can follow it), and it has no
// uuid or timestamp of its own.
func (p *parser) costState(_ *record, data []byte) bool {
	return p.Emit(session.Event{
		Kind:       session.KindSystem,
		Provenance: p.Prov(data),
		System: &session.SystemEvent{
			Subtype: "cost-state",
			Details: parseutil.CloneRaw(data),
		},
	})
}

// attachment is the model-visible part of an attachment record, for
// records without a rendered form. Which key carries the text varies by
// type: content on the older types (task_reminder, skill_listing,
// hook_success), text on the 2.1.27x reminders (model,
// total_tokens_reminder, batching_reminder_sent, silent_turn_reminder),
// and a type-specific field on the rest.
type attachment struct {
	Type    string          `json:"type"`
	Content json.RawMessage `json:"content"`
	Text    json.RawMessage `json:"text"`
	// SystemPrompt is prompt_snapshot's system prompt, one section per
	// element, with a __SYSTEM_PROMPT_DYNAMIC_BOUNDARY__ sentinel between
	// the cached and the per-turn sections.
	SystemPrompt []string `json:"systemPrompt"`
	// Files is instructions' CLAUDE.md set (path, type, content).
	Files []struct {
		Content string `json:"content"`
	} `json:"files"`
	// Prompt is queued_command's delivered prompt: a string, or content
	// blocks when it carries a pasted image. commandMode "prompt" with
	// origin.kind "human" is text the user typed while the model was
	// working; "task-notification" is a background task's report. The
	// corpus shows the attachment is the only record of the text (30 of
	// 31 never reappear as a user record).
	Prompt json.RawMessage `json:"prompt"`
	// Snippet is edited_text_file's numbered excerpt; Banner is
	// read_truncation_notice's notice.
	Snippet string `json:"snippet"`
	Banner  string `json:"banner"`
	// AddedLines are the listing lines agent_listing_delta and
	// deferred_tools_delta inject; AddedBlocks the instruction blocks of
	// mcp_instructions_delta.
	AddedLines  []string `json:"addedLines"`
	AddedBlocks []string `json:"addedBlocks"`
}

func (p *parser) attachment(rec *record, data []byte) bool {
	if len(rec.Attachment) == 0 {
		return p.UnknownOrFail(rec.Type, data, "attachment record has no attachment")
	}
	var att attachment
	if err := json.Unmarshal(rec.Attachment, &att); err != nil {
		return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("malformed attachment: %v", err))
	}
	return p.Emit(session.Event{
		Kind:       session.KindSystem,
		Timestamp:  parseutil.ParseTime(rec.Timestamp),
		ID:         rec.UUID,
		ParentID:   rec.ParentUUID,
		Provenance: p.Prov(data),
		System: &session.SystemEvent{
			Subtype: "attachment/" + att.Type,
			Text:    p.attachmentText(&att, rec),
			// The whole record, as for system records: the envelope's
			// rendered[].content (the delivered form) rides along.
			Details: parseutil.CloneRaw(data),
		},
	})
}

// attachmentText returns the attachment's model-visible text. When the
// record carries its rendered form (2.1.267+), that is the text: verbatim
// in the delivered form, with the <system-reminder> tags stripped in the
// bare form. The rendered block is the injected message itself, and the
// attachment's own fields do not always reconstruct it (deferred_tools_delta
// renders two lists from two fields; instructions adds a per-file header),
// so the fields are the fallback for records without it: content, text,
// or the type's own field. Types with neither (deferred_tools_record,
// prompt_render_point, ...) yield "".
func (p *parser) attachmentText(att *attachment, rec *record) string {
	rendered := rec.Rendered
	if att.Type == "queued_command" && p.inHumanTurn && len(rec.RenderedInHumanTurn) > 0 {
		rendered = rec.RenderedInHumanTurn
	}
	if len(rendered) > 0 {
		if p.Opts.TextForm == harness.TextDelivered {
			return joinRendered(rendered, func(s string) string { return s })
		}
		if s := joinRendered(rendered, stripReminder); s != "" {
			return s
		}
	}
	return bareAttachmentText(att)
}

// joinRendered joins rendered entries with a blank line. Each entry is
// one injected message; through 2.1.274 every attachment renderer emits
// at most one text message (the one two-message renderer, directory,
// emits a tool pair the harness does not record as rendered), so the
// join is defined but has no observed multi-entry case.
func joinRendered(rendered []struct {
	Content string `json:"content"`
}, form func(string) string,
) string {
	var parts []string
	for _, r := range rendered {
		if s := form(r.Content); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}

// isTurnMarker reports whether blocks are a single one-line bracketed
// text such as "[Request interrupted by user]": the harness skips these
// meta records when deciding whether a queued notification sits in the
// human's turn.
func isTurnMarker(blocks []block) bool {
	if len(blocks) == 0 {
		return false
	}
	for _, b := range blocks {
		if b.Type != "text" || !strings.HasPrefix(b.Text, "[") || !strings.HasSuffix(b.Text, "]") || strings.Contains(b.Text, "\n") {
			return false
		}
	}
	return true
}

// stripReminder removes the <system-reminder> wrapper around a rendered
// attachment; content without the wrapper is returned unchanged.
func stripReminder(s string) string {
	if body, ok := strings.CutPrefix(s, "<system-reminder>\n"); ok {
		if body, ok := strings.CutSuffix(body, "\n</system-reminder>"); ok {
			return body
		}
	}
	return s
}

func bareAttachmentText(att *attachment) string {
	var s string
	if len(att.Content) > 0 && json.Unmarshal(att.Content, &s) == nil && s != "" {
		return s
	}
	if len(att.Text) > 0 && json.Unmarshal(att.Text, &s) == nil && s != "" {
		return s
	}
	switch att.Type {
	case "prompt_snapshot":
		return joinPromptSections(att.SystemPrompt)
	case "instructions":
		parts := make([]string, 0, len(att.Files))
		for _, f := range att.Files {
			if f.Content != "" {
				parts = append(parts, f.Content)
			}
		}
		return strings.Join(parts, "\n\n")
	case "queued_command":
		if len(att.Prompt) == 0 {
			return ""
		}
		blocks, err := parseBlocks(att.Prompt)
		if err != nil {
			return ""
		}
		return blocksPlainText(blocks)
	case "edited_text_file":
		return att.Snippet
	case "read_truncation_notice":
		return att.Banner
	case "agent_listing_delta", "deferred_tools_delta":
		return strings.Join(att.AddedLines, "\n")
	case "mcp_instructions_delta":
		return strings.Join(att.AddedBlocks, "\n\n")
	}
	return ""
}

// promptBoundary separates the cached from the per-turn sections of a
// prompt_snapshot's systemPrompt; a harness marker, not prompt text.
const promptBoundary = "__SYSTEM_PROMPT_DYNAMIC_BOUNDARY__"

// joinPromptSections concatenates a prompt_snapshot's sections the way the
// harness builds the API's system blocks (verified in the 2.1.274 bundle):
// empty sections and the boundary sentinel are skipped and the rest are
// joined with a blank line. The harness hoists its identity block ahead
// of the others and splits at the boundary into separately cached
// blocks, so this is the prompt's text, not its exact block layout.
func joinPromptSections(sections []string) string {
	var parts []string
	for _, s := range sections {
		if s != "" && s != promptBoundary {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}

// parseBlocks decodes API message content, which is either a bare string
// (one text block) or an array of typed blocks.
func parseBlocks(raw json.RawMessage) ([]block, error) {
	return parseutil.ParseBlocks(raw,
		func(s string) block { return block{Type: "text", Text: s} },
		func(b *block, r json.RawMessage) { b.raw = r })
}

// toContentBlock maps a native block to a schema content block. Non-text
// kinds keep the native block verbatim in Raw so no payload is lost.
func toContentBlock(b *block) session.ContentBlock {
	switch b.Type {
	case "text":
		return session.ContentBlock{Kind: session.ContentText, Text: b.Text}
	case "image":
		return session.ContentBlock{Kind: session.ContentImage, Raw: b.raw}
	case "document":
		return session.ContentBlock{Kind: session.ContentDocument, Raw: b.raw}
	default:
		return session.ContentBlock{Kind: session.ContentOther, Raw: b.raw}
	}
}

func blocksPlainText(blocks []block) string {
	var buf bytes.Buffer
	for i := range blocks {
		if blocks[i].Type == "text" && blocks[i].Text != "" {
			if buf.Len() > 0 {
				buf.WriteByte('\n')
			}
			buf.WriteString(blocks[i].Text)
		}
	}
	return buf.String()
}

// parseUsage decodes a usage object, promoting the token counts and
// preserving everything else (service_tier, inference_geo, ...) in Extra.
func parseUsage(raw json.RawMessage) (*session.TokenUsage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	u := &session.TokenUsage{}
	take := func(key string, dst *int64) error {
		v, ok := m[key]
		if !ok {
			return nil
		}
		delete(m, key)
		return json.Unmarshal(v, dst)
	}
	for key, dst := range map[string]*int64{
		"input_tokens":                &u.InputTokens,
		"output_tokens":               &u.OutputTokens,
		"cache_read_input_tokens":     &u.CacheReadInputTokens,
		"cache_creation_input_tokens": &u.CacheCreationInputTokens,
	} {
		if err := take(key, dst); err != nil {
			return nil, err
		}
	}
	if len(m) > 0 {
		u.Extra = m
	}
	return u, nil
}

// extractFetch promotes retrieval metrics from a fetch-tool enrichment
// sidecar (Claude Code's WebFetch toolUseResult carries url, bytes, code,
// durationMs).
func extractFetch(raw json.RawMessage) *session.FetchInfo {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil
	}
	var s struct {
		URL        string `json:"url"`
		Bytes      int64  `json:"bytes"`
		Code       int    `json:"code"`
		DurationMS int64  `json:"durationMs"`
	}
	if err := json.Unmarshal(raw, &s); err != nil || s.URL == "" {
		return nil
	}
	return &session.FetchInfo{
		URL:        s.URL,
		RawBytes:   s.Bytes,
		StatusCode: s.Code,
		DurationMS: s.DurationMS,
	}
}
