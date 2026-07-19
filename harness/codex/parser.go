package codex

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

// rolloutLine is the envelope of every rollout record.
type rolloutLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// sessionMeta is the payload of a session_meta record. Field presence
// varies by version: 0.118 has id only, 0.144 adds session_id.
type sessionMeta struct {
	SessionID  string `json:"session_id"`
	ID         string `json:"id"`
	CWD        string `json:"cwd"`
	CLIVersion string `json:"cli_version"`
	Git        struct {
		Branch string `json:"branch"`
	} `json:"git"`
}

// responseItem covers all response_item payload variants; unused fields
// stay zero for any given type.
type responseItem struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`

	// reasoning
	Summary          json.RawMessage `json:"summary"`
	EncryptedContent string          `json:"encrypted_content"`

	// tool calls and outputs
	ID        string          `json:"id"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Input     string          `json:"input"`     // custom_tool_call: opaque string
	Arguments string          `json:"arguments"` // function_call: JSON-encoded string
	Output    json.RawMessage `json:"output"`
	Status    string          `json:"status"`
	Action    json.RawMessage `json:"action"` // local_shell_call, web_search_call
	Success   *bool           `json:"success"`
}

// contentBlock is one element of a message/output content array.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`

	raw json.RawMessage
}

// pendingAnchor holds an assistant message until its usage is known: the
// fold closes on the next response_item, a turn boundary, or EOF, taking
// the pooled token usage with it.
type pendingAnchor struct {
	line  int
	ts    *time.Time
	model string
	text  []session.ContentBlock
	raw   []json.RawMessage
}

type parser struct {
	parseutil.Emitter
	meta bool
	// model is the current model from the latest turn_context; rollout
	// assistant messages do not carry one.
	model string
	// toolNames maps call IDs to tool names, for denormalizing results.
	toolNames map[string]string
	fold      *pendingAnchor
	// pending pools last_token_usage from token_count events until an
	// assistant anchor closes and takes it. token_count fires per API
	// request, so a turn's tool-loop requests are attributed to the
	// message they produced; per-request snapshots sum to the session
	// total (verified empirically). Two accepted edges: usage still
	// pooled at EOF with no open anchor (an aborted turn) is attributed
	// to no message and absent from Totals, and a turn that produced no
	// assistant message leaves its usage pooled for the next turn's
	// anchor. Both remain recoverable from the token_count system
	// events' Details.
	pending *session.TokenUsage
}

// Events implements harness.Adapter.
func (Adapter) Events(r io.Reader, opts harness.Options) iter.Seq2[session.Event, error] {
	return func(yield func(session.Event, error) bool) {
		p := &parser{
			Emitter:   parseutil.Emitter{Harness: harness.Codex, Opts: opts, Yield: yield},
			toolNames: make(map[string]string),
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
	var rec rolloutLine
	if err := json.Unmarshal(data, &rec); err != nil {
		return p.UnknownOrFail("", data, fmt.Sprintf("invalid JSON: %v", err))
	}
	if !recordTypes[rec.Type] {
		return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("unrecognized record type %q", rec.Type))
	}
	if !p.meta {
		if !p.emitMeta(&rec, data) {
			return false
		}
		if rec.Type == "session_meta" {
			return true // the record became the session_meta event
		}
	} else if rec.Type == "session_meta" {
		// A later session_meta (resumed/forked session?) is preserved as
		// telemetry rather than re-emitted as meta.
		return p.system(&rec, data, "session_meta", "", "")
	}

	switch rec.Type {
	case "world_state", "compacted":
		return p.system(&rec, data, rec.Type, "", "")
	case "turn_context":
		var tc struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(rec.Payload, &tc); err == nil && tc.Model != "" {
			p.model = tc.Model
		}
		return p.system(&rec, data, "turn_context", "", "")
	case "event_msg":
		return p.eventMsg(&rec, data)
	case "response_item":
		return p.responseItem(&rec, data)
	}
	return true // unreachable; recordTypes is checked above
}

// emitMeta emits the session_meta event from the first record. Rollouts
// start with a session_meta record; if this one is not (resumed session?),
// the meta is sparse.
func (p *parser) emitMeta(rec *rolloutLine, data []byte) bool {
	p.meta = true
	m := &session.Meta{Harness: string(harness.Codex)}
	if rec.Type == "session_meta" {
		var sm sessionMeta
		if err := json.Unmarshal(rec.Payload, &sm); err != nil {
			msg := fmt.Sprintf("malformed session_meta: %v", err)
			if !p.Opts.Permissive {
				return p.UnknownOrFail(rec.Type, data, msg)
			}
			// Permissive mode still leads with a sparse session_meta so
			// the stream honors the meta-first contract, then accounts
			// for the malformed record itself as an unknown event.
			m.HarnessVersion = p.Opts.HarnessVersionHint
			if !p.Emit(session.Event{
				Kind:        session.KindSessionMeta,
				Timestamp:   parseutil.ParseTime(rec.Timestamp),
				Provenance:  p.Prov(data),
				SessionMeta: m,
			}) {
				return false
			}
			return p.UnknownOrFail(rec.Type, data, msg)
		}
		m.SessionID = sm.SessionID
		if m.SessionID == "" {
			m.SessionID = sm.ID
		}
		m.CWD = sm.CWD
		m.GitBranch = sm.Git.Branch
		p.Version = sm.CLIVersion
	}
	m.HarnessVersion = cmp.Or(p.Version, p.Opts.HarnessVersionHint)
	return p.Emit(session.Event{
		Kind:        session.KindSessionMeta,
		Timestamp:   parseutil.ParseTime(rec.Timestamp),
		Provenance:  p.Prov(data),
		SessionMeta: m,
	})
}

func (p *parser) eventMsg(rec *rolloutLine, data []byte) bool {
	var pl struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Info    *struct {
			LastTokenUsage json.RawMessage `json:"last_token_usage"`
		} `json:"info"`
	}
	if err := json.Unmarshal(rec.Payload, &pl); err != nil {
		return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("malformed event_msg: %v", err))
	}
	switch {
	case skipEventMsg(pl.Type):
		if p.Opts.OnSkip != nil {
			p.Opts.OnSkip(p.Line, "event_msg/"+pl.Type)
		}
		return true
	case pl.Type == "token_count":
		if pl.Info != nil {
			u, err := parseTokenUsage(pl.Info.LastTokenUsage)
			if err != nil {
				return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("malformed token usage: %v", err))
			}
			if u != nil {
				if p.pending == nil {
					p.pending = &session.TokenUsage{}
				}
				p.pending.Add(*u)
			}
		}
		return p.system(rec, data, "token_count", "", "")
	case pl.Type == "task_started" || pl.Type == "task_complete":
		// Turn boundaries close an open anchor before they are emitted,
		// so the assistant message lands inside its turn.
		if p.fold != nil && !p.closeFold() {
			return false
		}
		return p.system(rec, data, pl.Type, "", "")
	case pl.Type == "error" || pl.Type == "stream_error":
		return p.system(rec, data, pl.Type, "error", pl.Message)
	default:
		// Unknown event_msg subtypes are telemetry, preserved in full as
		// system events rather than failing: the vocabulary churns per
		// release and nothing model-visible is at stake.
		return p.system(rec, data, pl.Type, "", "")
	}
}

func (p *parser) responseItem(rec *rolloutLine, data []byte) bool {
	var item responseItem
	if err := json.Unmarshal(rec.Payload, &item); err != nil {
		return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("malformed response_item: %v", err))
	}
	// Any response item closes an open assistant anchor.
	if p.fold != nil && !p.closeFold() {
		return false
	}
	switch item.Type {
	case "message":
		return p.message(rec, &item, data)
	case "reasoning":
		return p.reasoning(rec, &item, data)
	case "function_call":
		p.toolNames[item.CallID] = item.Name
		return p.Emit(session.Event{
			Kind:       session.KindToolCall,
			Timestamp:  parseutil.ParseTime(rec.Timestamp),
			Provenance: p.Prov(data),
			ToolCall: &session.ToolCall{
				ToolCallID: item.CallID,
				Name:       item.Name,
				Kind:       kindFor(item.Name),
				Input:      rawFromMaybeJSON(item.Arguments),
			},
		})
	case "custom_tool_call":
		p.toolNames[item.CallID] = item.Name
		return p.Emit(session.Event{
			Kind:       session.KindToolCall,
			Timestamp:  parseutil.ParseTime(rec.Timestamp),
			Provenance: p.Prov(data),
			ToolCall: &session.ToolCall{
				ToolCallID: item.CallID,
				Name:       item.Name,
				Kind:       kindFor(item.Name),
				Input:      rawFromMaybeJSON(item.Input),
			},
		})
	case "local_shell_call":
		id := item.CallID
		if id == "" {
			id = item.ID
		}
		p.toolNames[id] = "local_shell"
		return p.Emit(session.Event{
			Kind:       session.KindToolCall,
			Timestamp:  parseutil.ParseTime(rec.Timestamp),
			Provenance: p.Prov(data),
			ToolCall: &session.ToolCall{
				ToolCallID: id,
				Name:       "local_shell",
				Kind:       session.ToolKindExecute,
				Input:      item.Action,
			},
		})
	case "function_call_output", "custom_tool_call_output":
		return p.toolResult(rec, &item, data)
	case "web_search_call":
		return p.webSearchCall(rec, &item, data)
	default:
		return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("unrecognized response_item type %q", item.Type))
	}
}

func (p *parser) message(rec *rolloutLine, item *responseItem, data []byte) bool {
	blocks, err := parseBlocks(item.Content)
	if err != nil {
		return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("malformed message content: %v", err))
	}
	switch item.Role {
	case "assistant":
		f := &pendingAnchor{
			line:  p.Line,
			ts:    parseutil.ParseTime(rec.Timestamp),
			model: p.model,
		}
		for i := range blocks {
			f.text = append(f.text, toContentBlock(&blocks[i]))
		}
		if p.Opts.KeepRaw {
			f.raw = []json.RawMessage{parseutil.CloneRaw(data)}
		}
		p.fold = f
		return true
	case "user":
		content := make([]session.ContentBlock, 0, len(blocks))
		for i := range blocks {
			content = append(content, toContentBlock(&blocks[i]))
		}
		return p.Emit(session.Event{
			Kind:       session.KindUserMessage,
			Timestamp:  parseutil.ParseTime(rec.Timestamp),
			Provenance: p.Prov(data),
			UserMessage: &session.UserMessage{
				Origin:  originOf(blocks),
				Content: content,
			},
		})
	case "developer", "system":
		return p.system(rec, data, "message/"+item.Role, "", blocksPlainText(blocks))
	default:
		return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("unrecognized message role %q", item.Role))
	}
}

func (p *parser) reasoning(rec *rolloutLine, item *responseItem, data []byte) bool {
	// Plaintext lives in content[] or summary[]; the encrypted blob is the
	// provider's opaque form of the thought and rides in Signature.
	text := joinBlockTexts(item.Content)
	if text == "" {
		text = joinBlockTexts(item.Summary)
	}
	return p.Emit(session.Event{
		Kind:       session.KindThinking,
		Timestamp:  parseutil.ParseTime(rec.Timestamp),
		Provenance: p.Prov(data),
		Thinking:   &session.Thinking{Text: text, Signature: item.EncryptedContent},
	})
}

func (p *parser) toolResult(rec *rolloutLine, item *responseItem, data []byte) bool {
	content, err := outputContent(item.Output)
	if err != nil {
		return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("malformed tool output: %v", err))
	}
	tr := &session.ToolResult{
		ToolCallID: item.CallID,
		ToolName:   p.toolNames[item.CallID],
		IsError:    isErrorOutput(item),
		Content:    content,
		Enrichment: rec.Payload,
	}
	p.Truncate(tr)
	return p.Emit(session.Event{
		Kind:       session.KindToolResult,
		Timestamp:  parseutil.ParseTime(rec.Timestamp),
		Provenance: p.Prov(data),
		ToolResult: tr,
	})
}

// webSearchCall maps a server-side search/fetch: status and action arrive
// in one record with no correlation ID and no retrieved content, so a
// synthesized call/result pair shares a line-derived ID.
func (p *parser) webSearchCall(rec *rolloutLine, item *responseItem, data []byte) bool {
	id := item.ID
	if id == "" {
		id = fmt.Sprintf("web_search_call:L%d", p.Line)
	}
	ts := parseutil.ParseTime(rec.Timestamp)
	call := session.Event{
		Kind:       session.KindToolCall,
		Timestamp:  ts,
		Provenance: p.Prov(data),
		ToolCall: &session.ToolCall{
			ToolCallID: id,
			Name:       "web_search",
			Kind:       session.ToolKindFetch,
			Input:      item.Action,
		},
	}
	if !p.Emit(call) {
		return false
	}
	return p.Emit(session.Event{
		Kind:       session.KindToolResult,
		Timestamp:  ts,
		Provenance: p.Prov(data),
		ToolResult: &session.ToolResult{
			ToolCallID: id,
			ToolName:   "web_search",
			IsError:    item.Status != "" && item.Status != "completed",
			Enrichment: rec.Payload,
		},
	})
}

// closeFold emits the pending assistant anchor with the pooled usage.
func (p *parser) closeFold() bool {
	f := p.fold
	p.fold = nil
	usage := p.pending
	p.pending = nil
	return p.Emit(session.Event{
		Kind:       session.KindAssistantMessage,
		Timestamp:  f.ts,
		Provenance: &session.Provenance{Line: f.line, EndLine: f.line, Raw: f.raw},
		AssistantMessage: &session.AssistantMessage{
			Model:   f.model,
			Content: f.text,
			Usage:   usage,
		},
	})
}

func (p *parser) system(rec *rolloutLine, data []byte, subtype, level, text string) bool {
	return p.Emit(session.Event{
		Kind:       session.KindSystem,
		Timestamp:  parseutil.ParseTime(rec.Timestamp),
		Provenance: p.Prov(data),
		System: &session.SystemEvent{
			Subtype: subtype,
			Level:   level,
			Text:    text,
			Details: rec.Payload,
		},
	})
}

// parseBlocks decodes a content array (or bare string) into blocks.
func parseBlocks(raw json.RawMessage) ([]contentBlock, error) {
	return parseutil.ParseBlocks(raw,
		func(s string) contentBlock { return contentBlock{Type: "text", Text: s} },
		func(b *contentBlock, r json.RawMessage) { b.raw = r })
}

func toContentBlock(b *contentBlock) session.ContentBlock {
	switch b.Type {
	case "text", "input_text", "output_text", "summary_text", "reasoning_text":
		return session.ContentBlock{Kind: session.ContentText, Text: b.Text}
	case "input_image", "image":
		return session.ContentBlock{Kind: session.ContentImage, Raw: b.raw}
	default:
		return session.ContentBlock{Kind: session.ContentOther, Raw: b.raw}
	}
}

func blocksPlainText(blocks []contentBlock) string {
	var buf bytes.Buffer
	for i := range blocks {
		b := toContentBlock(&blocks[i])
		if b.Kind == session.ContentText && b.Text != "" {
			if buf.Len() > 0 {
				buf.WriteByte('\n')
			}
			buf.WriteString(b.Text)
		}
	}
	return buf.String()
}

func joinBlockTexts(raw json.RawMessage) string {
	blocks, err := parseBlocks(raw)
	if err != nil {
		return ""
	}
	return blocksPlainText(blocks)
}

// outputContent decodes a tool output, which may be a plain string, a
// content-block array (0.144 custom tools), or an object.
func outputContent(raw json.RawMessage) ([]session.ContentBlock, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	if raw[0] == '{' {
		var obj struct {
			Content string `json:"content"`
			Output  string `json:"output"`
		}
		if err := json.Unmarshal(raw, &obj); err != nil {
			return nil, err
		}
		text := obj.Content
		if text == "" {
			text = obj.Output
		}
		if text == "" {
			return []session.ContentBlock{{Kind: session.ContentOther, Raw: raw}}, nil
		}
		return []session.ContentBlock{{Kind: session.ContentText, Text: text}}, nil
	}
	blocks, err := parseBlocks(raw)
	if err != nil {
		return nil, err
	}
	out := make([]session.ContentBlock, 0, len(blocks))
	for i := range blocks {
		out = append(out, toContentBlock(&blocks[i]))
	}
	return out, nil
}

// isErrorOutput detects failed tool calls: an explicit success=false, or a
// shell-style JSON payload with a nonzero exit code.
func isErrorOutput(item *responseItem) bool {
	if item.Success != nil {
		return !*item.Success
	}
	raw := bytes.TrimSpace(item.Output)
	if len(raw) == 0 {
		return false
	}
	var s string
	if raw[0] == '"' {
		if json.Unmarshal(raw, &s) != nil {
			return false
		}
		raw = []byte(s)
	}
	var inner struct {
		Metadata struct {
			ExitCode *int `json:"exit_code"`
		} `json:"metadata"`
	}
	if json.Unmarshal(raw, &inner) == nil && inner.Metadata.ExitCode != nil {
		return *inner.Metadata.ExitCode != 0
	}
	return false
}

// originOf classifies a user-role message: known harness-injected tag
// prefixes mark it as harness content, anything else is human.
func originOf(blocks []contentBlock) session.Origin {
	if len(blocks) == 0 {
		return session.OriginHuman
	}
	text := bytes.TrimSpace([]byte(blocks[0].Text))
	for _, prefix := range harnessTagPrefixes {
		if bytes.HasPrefix(text, []byte(prefix)) {
			return session.OriginHarness
		}
	}
	return session.OriginHuman
}

// rawFromMaybeJSON turns a tool input string into a JSON value: the parsed
// value when the string is itself JSON (function_call arguments), or a
// JSON-encoded string otherwise (custom_tool_call scripts).
func rawFromMaybeJSON(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	if json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	enc, err := json.Marshal(s)
	if err != nil {
		return nil
	}
	return enc
}

// parseTokenUsage decodes a last_token_usage object, promoting the core
// counts. reasoning_output_tokens and total_tokens are not promoted (they
// remain in the token_count system events) because pooled usage cannot sum
// Extra fields.
func parseTokenUsage(raw json.RawMessage) (*session.TokenUsage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var u struct {
		InputTokens       int64 `json:"input_tokens"`
		CachedInputTokens int64 `json:"cached_input_tokens"`
		OutputTokens      int64 `json:"output_tokens"`
	}
	if err := json.Unmarshal(raw, &u); err != nil {
		return nil, err
	}
	return &session.TokenUsage{
		InputTokens:          u.InputTokens,
		OutputTokens:         u.OutputTokens,
		CacheReadInputTokens: u.CachedInputTokens,
	}, nil
}
