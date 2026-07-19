package claudecode

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"time"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/parseutil"
	"github.com/agent-ecosystem/agentminutes/session"
)

// record is the top-level envelope of one transcript line.
type record struct {
	Type          string          `json:"type"`
	UUID          string          `json:"uuid"`
	ParentUUID    string          `json:"parentUuid"`
	Timestamp     string          `json:"timestamp"`
	SessionID     string          `json:"sessionId"`
	Version       string          `json:"version"`
	CWD           string          `json:"cwd"`
	GitBranch     string          `json:"gitBranch"`
	IsSidechain   bool            `json:"isSidechain"`
	AgentID       string          `json:"agentId"`
	IsMeta        bool            `json:"isMeta"`
	Subtype       string          `json:"subtype"`
	Level         string          `json:"level"`
	Content       json.RawMessage `json:"content"`
	Message       json.RawMessage `json:"message"`
	Attachment    json.RawMessage `json:"attachment"`
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

func (p *parser) attachment(rec *record, data []byte) bool {
	if len(rec.Attachment) == 0 {
		return p.UnknownOrFail(rec.Type, data, "attachment record has no attachment")
	}
	var att struct {
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(rec.Attachment, &att); err != nil {
		return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("malformed attachment: %v", err))
	}
	var text string
	if len(att.Content) > 0 {
		_ = json.Unmarshal(att.Content, &text)
	}
	return p.Emit(session.Event{
		Kind:       session.KindSystem,
		Timestamp:  parseutil.ParseTime(rec.Timestamp),
		ID:         rec.UUID,
		ParentID:   rec.ParentUUID,
		Provenance: p.Prov(data),
		System: &session.SystemEvent{
			Subtype: "attachment/" + att.Type,
			Text:    text,
			Details: rec.Attachment,
		},
	})
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
