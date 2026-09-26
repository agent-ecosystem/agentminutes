package copilot

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"strings"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/parseutil"
	"github.com/agent-ecosystem/agentminutes/session"
)

// envelope is the shape of every events.jsonl record. parentId links each
// record to the previous one (null on the first), so a file is a chain.
// agentId is present only on records written by a subagent (its
// conversation is interleaved in the parent transcript).
type envelope struct {
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	ID        string          `json:"id"`
	Timestamp string          `json:"timestamp"`
	ParentID  string          `json:"parentId"`
	AgentID   string          `json:"agentId"`
}

// sessionStart is the data of a session.start record. context carries the
// cwd always and the git identity (gitRoot, repository, branch, headCommit,
// ...) when the cwd is inside a repository.
type sessionStart struct {
	SessionID      string `json:"sessionId"`
	CopilotVersion string `json:"copilotVersion"`
	Context        struct {
		CWD    string `json:"cwd"`
		Branch string `json:"branch"`
	} `json:"context"`
}

// userMessage is the data of a user.message record. content is the text
// the user submitted; transformedContent is what the model was actually
// sent (content wrapped in a <current_datetime> preamble), left in the raw
// record rather than emitted, since the wrapper is harness framing rather
// than user intent. source is "agent-<session id>" on a subagent's
// prompt, which the parent agent authored.
type userMessage struct {
	Content string `json:"content"`
	Source  string `json:"source"`
}

// agentSourcePrefix marks a user.message authored by an agent rather than
// the human: the delegated prompt a subagent receives.
const agentSourcePrefix = "agent-"

// systemMessage is the data of a system.message record: the system prompt
// as sent, with content the concatenation of contentBlocks.
type systemMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// assistantMessage is the data of an assistant.message record: one API
// message, with its text, tool requests, and (on reasoning turns) the
// provider-native reasoning blocks plus the harness's flattened
// reasoningText/reasoningOpaque pair.
type assistantMessage struct {
	MessageID       string          `json:"messageId"`
	Model           string          `json:"model"`
	Content         string          `json:"content"`
	ToolRequests    []toolRequest   `json:"toolRequests"`
	ReasoningText   string          `json:"reasoningText"`
	ReasoningOpaque string          `json:"reasoningOpaque"`
	ReasoningBlocks *reasoningBlock `json:"reasoningBlocks"`
}

type toolRequest struct {
	ToolCallID string          `json:"toolCallId"`
	Name       string          `json:"name"`
	Arguments  json.RawMessage `json:"arguments"`
}

// reasoningBlock is the provider-native reasoning. Observed providers:
// "anthropic-messages" (blocks of type thinking: thinking text plus a
// signature) and "openai-responses" (blocks of type reasoning: content
// and summary arrays of {type, text} items, plus encrypted_content).
type reasoningBlock struct {
	Provider string `json:"provider"`
	Blocks   []struct {
		Type string `json:"type"`

		// anthropic-messages
		Thinking  string `json:"thinking"`
		Signature string `json:"signature"`

		// openai-responses
		Content          []reasoningItem `json:"content"`
		Summary          []reasoningItem `json:"summary"`
		EncryptedContent string          `json:"encrypted_content"`
	} `json:"blocks"`
}

type reasoningItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func joinItems(items []reasoningItem) string {
	var parts []string
	for _, it := range items {
		if it.Text != "" {
			parts = append(parts, it.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// toolExecution covers tool.execution_start (toolName, arguments) and
// tool.execution_complete (success, result or error, shell exit code).
type toolExecution struct {
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	Success    *bool  `json:"success"`
	Result     *struct {
		Content json.RawMessage `json:"content"`
		// BinaryResultsForLlm lists binary content the model received
		// alongside the text (an image viewed with view); each entry
		// references a session.binary_asset record by assetId.
		BinaryResultsForLlm []binaryResult `json:"binaryResultsForLlm"`
	} `json:"result"`
	ShellExecution *struct {
		ExitCode int `json:"exitCode"`
	} `json:"shellExecution"`
	Error *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
	// ToolTelemetry carries per-tool sidecar data; web_fetch records the
	// URL and HTTP status there (restrictedProperties.url,
	// metrics.httpStatusCode).
	ToolTelemetry *struct {
		RestrictedProperties struct {
			URL string `json:"url"`
		} `json:"restrictedProperties"`
		Metrics struct {
			HTTPStatusCode int `json:"httpStatusCode"`
		} `json:"metrics"`
	} `json:"toolTelemetry"`
}

type binaryResult struct {
	Type     string `json:"type"`
	MimeType string `json:"mimeType"`

	raw json.RawMessage
}

func (b *binaryResult) UnmarshalJSON(data []byte) error {
	type plain binaryResult
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*b = binaryResult(p)
	b.raw = parseutil.CloneRaw(data)
	return nil
}

type parser struct {
	parseutil.Emitter
	meta bool
	// pendingSkill holds a skill.invoked event back until the next record
	// (delivered text form only): when that record is the
	// skill.context_delivered_ref whose contentId hashes the body, the
	// event's Text becomes prefix + body + suffix, the wrapper the
	// harness reports delivering; otherwise it goes out bare.
	pendingSkill        *session.Event
	pendingSkillContent string
	// toolNames maps tool call IDs to tool names, for denormalizing
	// results. Populated from assistant.message toolRequests (the call)
	// and tool.execution_start (so an orphan result still names its tool).
	toolNames map[string]string
}

// Events implements harness.Adapter.
func (Adapter) Events(r io.Reader, opts harness.Options) iter.Seq2[session.Event, error] {
	return func(yield func(session.Event, error) bool) {
		p := &parser{
			Emitter:   parseutil.Emitter{Harness: harness.Copilot, Opts: opts, Yield: yield},
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
		p.flushSkill(nil)
	}
}

func (p *parser) record(data []byte) bool {
	var rec envelope
	if err := json.Unmarshal(data, &rec); err != nil {
		if !p.flushSkill(nil) {
			return false
		}
		return p.UnknownOrFail("", data, fmt.Sprintf("invalid JSON: %v", err))
	}
	if !p.flushSkill(&rec) {
		return false
	}
	if !p.meta {
		if !p.emitMeta(&rec, data) {
			return false
		}
		if rec.Type == "session.start" {
			return true // the record became the session_meta event
		}
	}
	switch rec.Type {
	case "session.start":
		// A second session.start is unobserved (resumes write
		// session.resume); preserve it as telemetry rather than re-emit
		// meta.
		return p.system(&rec, data, "")
	case "user.message":
		return p.userMessage(&rec, data)
	case "system.message":
		var sm systemMessage
		if err := json.Unmarshal(rec.Data, &sm); err != nil {
			return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("malformed system.message: %v", err))
		}
		return p.system(&rec, data, sm.Content)
	case "assistant.message":
		return p.assistantMessage(&rec, data)
	case "tool.execution_start":
		var te toolExecution
		if err := json.Unmarshal(rec.Data, &te); err != nil {
			return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("malformed tool.execution_start: %v", err))
		}
		if te.ToolCallID != "" && te.ToolName != "" {
			if _, known := p.toolNames[te.ToolCallID]; !known {
				p.toolNames[te.ToolCallID] = te.ToolName
			}
		}
		return p.system(&rec, data, "")
	case "tool.execution_complete":
		return p.toolResult(&rec, data)
	case "abort":
		var ab struct {
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(rec.Data, &ab); err != nil {
			return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("malformed abort: %v", err))
		}
		return p.system(&rec, data, ab.Reason)
	case "session.info":
		var si struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(rec.Data, &si); err != nil {
			return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("malformed session.info: %v", err))
		}
		return p.system(&rec, data, si.Message)
	case "skill.invoked":
		// The SKILL.md body the skill tool delivered (frontmatter
		// stripped). The following skill.context_delivered_ref hashes
		// exactly this content and records the <skill-context> wrapper
		// it was delivered in; the wrapper stays in that record's
		// details. The role the model received it in is not recorded,
		// so this stays a system event with the body as its text rather
		// than a synthesized user_message (Claude Code's vehicle).
		var si struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(rec.Data, &si); err != nil {
			return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("malformed skill.invoked: %v", err))
		}
		if p.Opts.TextForm == harness.TextDelivered && si.Content != "" {
			ev := p.systemEvent(&rec, data, si.Content)
			p.pendingSkill = &ev
			p.pendingSkillContent = si.Content
			return true
		}
		return p.system(&rec, data, si.Content)
	}
	if recordTypes[rec.Type] || isTelemetryType(rec.Type) {
		// Turn boundaries, permission prompts, usage checkpoints, shutdown
		// and resume records, subagent lifecycle records, plus any
		// telemetry subtype this release does not know: preserved in
		// full.
		return p.system(&rec, data, "")
	}
	return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("unrecognized record type %q", rec.Type))
}

// emitMeta emits the session_meta event from the first record. Transcripts
// start with session.start; if this one does not (a tail-only copy?), the
// meta is sparse and the record is then handled normally.
func (p *parser) emitMeta(rec *envelope, data []byte) bool {
	p.meta = true
	m := &session.Meta{Harness: string(harness.Copilot)}
	if rec.Type == "session.start" {
		var ss sessionStart
		if err := json.Unmarshal(rec.Data, &ss); err != nil {
			msg := fmt.Sprintf("malformed session.start: %v", err)
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
				ID:          rec.ID,
				ParentID:    rec.ParentID,
				AgentID:     rec.AgentID,
				Provenance:  p.Prov(data),
				SessionMeta: m,
			}) {
				return false
			}
			return p.UnknownOrFail(rec.Type, data, msg)
		}
		m.SessionID = ss.SessionID
		m.CWD = ss.Context.CWD
		m.GitBranch = ss.Context.Branch
		p.Version = ss.CopilotVersion
	}
	m.HarnessVersion = cmp.Or(p.Version, p.Opts.HarnessVersionHint)
	ev := session.Event{
		Kind:        session.KindSessionMeta,
		Timestamp:   parseutil.ParseTime(rec.Timestamp),
		SessionMeta: m,
	}
	if rec.Type == "session.start" {
		ev.ID = rec.ID
		ev.ParentID = rec.ParentID
		ev.AgentID = rec.AgentID
		ev.Provenance = p.Prov(data)
	} else {
		// Sparse meta synthesized ahead of a non-start first record: it
		// shares the record's line so accounting holds, but the record
		// itself is emitted separately.
		ev.Provenance = &session.Provenance{Line: p.Line, EndLine: p.Line}
	}
	return p.Emit(ev)
}

func (p *parser) userMessage(rec *envelope, data []byte) bool {
	var um userMessage
	if err := json.Unmarshal(rec.Data, &um); err != nil {
		return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("malformed user.message: %v", err))
	}
	// The human's own prompts carry no source; a subagent's prompt is
	// authored by the parent agent (source "agent-<session id>") and is
	// not user intent. The harness's own framing lives in
	// transformedContent (not emitted) and system.message records.
	origin := session.OriginHuman
	if strings.HasPrefix(um.Source, agentSourcePrefix) {
		origin = session.OriginHarness
	}
	return p.Emit(session.Event{
		Kind:       session.KindUserMessage,
		Timestamp:  parseutil.ParseTime(rec.Timestamp),
		ID:         rec.ID,
		ParentID:   rec.ParentID,
		AgentID:    rec.AgentID,
		Provenance: p.Prov(data),
		UserMessage: &session.UserMessage{
			Origin:  origin,
			Content: []session.ContentBlock{{Kind: session.ContentText, Text: um.Content}},
		},
	})
}

// assistantMessage emits one API message's events: its reasoning blocks as
// thinking events, its tool requests as tool calls, then the accounting
// anchor, all sharing the record's messageId.
func (p *parser) assistantMessage(rec *envelope, data []byte) bool {
	var am assistantMessage
	if err := json.Unmarshal(rec.Data, &am); err != nil {
		return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("malformed assistant.message: %v", err))
	}
	ts := parseutil.ParseTime(rec.Timestamp)
	base := func() session.Event {
		return session.Event{
			Timestamp:  ts,
			ID:         rec.ID,
			ParentID:   rec.ParentID,
			AgentID:    rec.AgentID,
			MessageID:  am.MessageID,
			Provenance: p.Prov(data),
		}
	}

	switch {
	case am.ReasoningBlocks != nil:
		// Provider-native reasoning (observed provider
		// "anthropic-messages": thinking blocks with signatures).
		for _, b := range am.ReasoningBlocks.Blocks {
			var th session.Thinking
			switch b.Type {
			case "thinking":
				th = session.Thinking{Text: b.Thinking, Signature: b.Signature}
			case "reasoning":
				// Plaintext, when any, is in content[] or summary[]; the
				// encrypted blob is the provider's opaque form of the
				// thought and rides in Signature (the Codex precedent).
				text := joinItems(b.Content)
				if text == "" {
					text = joinItems(b.Summary)
				}
				th = session.Thinking{Text: text, Signature: b.EncryptedContent}
			default:
				return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("unrecognized reasoning block type %q (provider %q)", b.Type, am.ReasoningBlocks.Provider))
			}
			ev := base()
			ev.Kind = session.KindThinking
			ev.Thinking = &th
			if !p.Emit(ev) {
				return false
			}
		}
	case am.ReasoningText != "" || am.ReasoningOpaque != "":
		// The harness's flattened form, for providers whose native
		// blocks are not recorded (unobserved; the pair has only been
		// seen alongside reasoningBlocks).
		ev := base()
		ev.Kind = session.KindThinking
		ev.Thinking = &session.Thinking{Text: am.ReasoningText, Signature: am.ReasoningOpaque}
		if !p.Emit(ev) {
			return false
		}
	}

	for _, tr := range am.ToolRequests {
		p.toolNames[tr.ToolCallID] = tr.Name
		ev := base()
		ev.Kind = session.KindToolCall
		ev.ToolCall = &session.ToolCall{
			ToolCallID: tr.ToolCallID,
			Name:       tr.Name,
			Kind:       kindFor(tr.Name),
			Input:      parseutil.CloneRaw(tr.Arguments),
		}
		if !p.Emit(ev) {
			return false
		}
	}

	var content []session.ContentBlock
	if am.Content != "" {
		content = []session.ContentBlock{{Kind: session.ContentText, Text: am.Content}}
	}
	ev := base()
	ev.Kind = session.KindAssistantMessage
	ev.AssistantMessage = &session.AssistantMessage{
		Model:   am.Model,
		Content: content,
		// Usage stays nil: the format records no per-message usage. The
		// session-cumulative per-model totals in session.shutdown ride
		// in that record's system event.
	}
	return p.Emit(ev)
}

func (p *parser) toolResult(rec *envelope, data []byte) bool {
	var te toolExecution
	if err := json.Unmarshal(rec.Data, &te); err != nil {
		return p.UnknownOrFail(rec.Type, data, fmt.Sprintf("malformed tool.execution_complete: %v", err))
	}
	tr := &session.ToolResult{
		ToolCallID: te.ToolCallID,
		ToolName:   p.toolNames[te.ToolCallID],
		// A shell command that exited nonzero is success: true to the
		// harness (the tool ran) but a failed call to the model, as the
		// other adapters classify it.
		IsError: (te.Success != nil && !*te.Success) ||
			(te.ShellExecution != nil && te.ShellExecution.ExitCode != 0),
		Enrichment: parseutil.CloneRaw(rec.Data),
	}
	if te.Result != nil {
		tr.Content = resultContent(te.Result.Content)
		for i := range te.Result.BinaryResultsForLlm {
			b := &te.Result.BinaryResultsForLlm[i]
			kind := session.ContentOther
			if b.Type == "image" {
				kind = session.ContentImage
			}
			// The descriptor (assetId, byte length, description) is the
			// block; the bytes are in the session.binary_asset system
			// event with the same assetId.
			tr.Content = append(tr.Content, session.ContentBlock{Kind: kind, MimeType: b.MimeType, Raw: b.raw})
		}
	}
	if te.Error != nil {
		tr.IsError = true
		if len(tr.Content) == 0 && te.Error.Message != "" {
			// A failed call feeds the model the error message.
			tr.Content = []session.ContentBlock{{Kind: session.ContentText, Text: te.Error.Message}}
		}
	}
	if kindFor(tr.ToolName) == session.ToolKindFetch && te.ToolTelemetry != nil {
		// Retrieval metrics promote to Fetch. RawBytes stays unset: the
		// telemetry's originalContentLength measures the converted
		// (markdown) content before truncation, not the resource as
		// fetched; it remains in the enrichment.
		if url, status := te.ToolTelemetry.RestrictedProperties.URL, te.ToolTelemetry.Metrics.HTTPStatusCode; url != "" || status != 0 {
			tr.Fetch = &session.FetchInfo{URL: url, StatusCode: status}
		}
	}
	p.Truncate(tr)
	return p.Emit(session.Event{
		Kind:       session.KindToolResult,
		Timestamp:  parseutil.ParseTime(rec.Timestamp),
		ID:         rec.ID,
		ParentID:   rec.ParentID,
		AgentID:    rec.AgentID,
		Provenance: p.Prov(data),
		ToolResult: tr,
	})
}

// resultContent maps a result's content: a string (the only observed
// shape) becomes one text block; any other non-null value is preserved
// verbatim as an "other" block rather than dropped.
func resultContent(raw json.RawMessage) []session.ContentBlock {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []session.ContentBlock{{Kind: session.ContentText, Text: s}}
	}
	return []session.ContentBlock{{Kind: session.ContentOther, Raw: parseutil.CloneRaw(raw)}}
}

// flushSkill emits a held skill.invoked event ahead of the record that
// follows it (nil at end of input). When next is the delivery record for
// that body (skill.context_delivered_ref whose contentId is the body's
// SHA-256), the text goes out wrapped in the recorded prefix and suffix;
// any other next record, a hash mismatch, or end of input sends it bare.
func (p *parser) flushSkill(next *envelope) bool {
	ev := p.pendingSkill
	if ev == nil {
		return true
	}
	p.pendingSkill = nil
	if next != nil && next.Type == "skill.context_delivered_ref" {
		var ref struct {
			ContentID string `json:"contentId"`
			Prefix    string `json:"prefix"`
			Suffix    string `json:"suffix"`
		}
		if json.Unmarshal(next.Data, &ref) == nil && ref.ContentID == contentHash(p.pendingSkillContent) {
			ev.System.Text = ref.Prefix + p.pendingSkillContent + ref.Suffix
		}
	}
	return p.Emit(*ev)
}

// contentHash is the skill.context_delivered_ref contentId form of a body.
func contentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// system preserves a record as a system event: the native type is the
// subtype and data rides verbatim in Details.
func (p *parser) system(rec *envelope, data []byte, text string) bool {
	return p.Emit(p.systemEvent(rec, data, text))
}

func (p *parser) systemEvent(rec *envelope, data []byte, text string) session.Event {
	return session.Event{
		Kind:       session.KindSystem,
		Timestamp:  parseutil.ParseTime(rec.Timestamp),
		ID:         rec.ID,
		ParentID:   rec.ParentID,
		AgentID:    rec.AgentID,
		Provenance: p.Prov(data),
		System: &session.SystemEvent{
			Subtype: rec.Type,
			Text:    text,
			Details: parseutil.CloneRaw(rec.Data),
		},
	}
}
