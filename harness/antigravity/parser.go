package antigravity

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/parseutil"
	"github.com/agent-ecosystem/agentminutes/session"
)

// record is one step record of a transcript_full.jsonl.
type record struct {
	StepIndex *int   `json:"step_index"`
	Source    string `json:"source"`
	Type      string `json:"type"`
	CreatedAt string `json:"created_at"`
	Content   string `json:"content"`
	Thinking  string `json:"thinking"`
	Error     string `json:"error"`
	// ErrorCode is numeric in observed data; raw tolerates either shape.
	ErrorCode json.RawMessage `json:"error_code"`
	ToolCalls []toolCall      `json:"tool_calls"`

	line     int
	step     int
	raw      json.RawMessage
	parseErr string
}

type toolCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// pendingCall tracks an emitted tool_call awaiting its result. Records
// carry no correlation IDs, so pairing is positional: results consume
// pending calls FIFO (empirically, a call at step N is answered at N+1).
type pendingCall struct {
	id   string
	name string
}

type parser struct {
	parseutil.Emitter
	// model is the human-readable model label extracted from the
	// USER_SETTINGS_CHANGE text; transcripts carry no model ID.
	model   string
	meta    bool
	pending []pendingCall
}

// modelRe extracts the model label from settings-change text like:
// "The user changed setting `Model Selection` from None to Gemini 3.5
// Flash (Medium). No need to comment..."
var modelRe = regexp.MustCompile("`Model Selection` from .*? to (.+?)\\.\\s")

// Events implements harness.Adapter.
//
// Antigravity appends some records out of step order (observed: a
// CHECKPOINT written after later steps), so the whole transcript is
// buffered and sorted by step_index before events are emitted. Streaming
// therefore does not run in constant memory for this harness; provenance
// records the original file line of every record.
func (Adapter) Events(r io.Reader, opts harness.Options) iter.Seq2[session.Event, error] {
	return func(yield func(session.Event, error) bool) {
		p := &parser{Emitter: parseutil.Emitter{Harness: harness.Antigravity, Opts: opts, Yield: yield}}
		recs, ok := p.readAll(r)
		if !ok {
			return
		}
		slices.SortStableFunc(recs, func(a, b record) int { return cmp.Compare(a.step, b.step) })
		for i := range recs {
			// Records are emitted in step order, not file order; keep the
			// emitter's line context pointing at the current record.
			p.Line = recs[i].line
			if !p.meta {
				if !p.emitMeta(&recs[i]) {
					return
				}
			}
			if !p.record(&recs[i]) {
				return
			}
		}
	}
}

// readAll buffers and envelope-parses every line. Malformed lines fail
// immediately in strict mode; in permissive mode they become records with
// parseErr set, sorted to the end of the stream.
func (p *parser) readAll(r io.Reader) ([]record, bool) {
	var recs []record
	sc := parseutil.NewLineScanner(r)
	for sc.Scan() {
		data := sc.Bytes()
		var rec record
		if err := json.Unmarshal(data, &rec); err != nil {
			if !p.Opts.Permissive {
				p.Line = sc.Line()
				p.Fail(fmt.Sprintf("invalid JSON: %v", err), nil)
				return nil, false
			}
			rec = record{parseErr: fmt.Sprintf("invalid JSON: %v", err)}
		}
		rec.line = sc.Line()
		rec.step = math.MaxInt
		if rec.StepIndex != nil {
			rec.step = *rec.StepIndex
		}
		rec.raw = parseutil.CloneRaw(data)
		recs = append(recs, rec)
	}
	if err := sc.Err(); err != nil {
		p.Line = sc.Line()
		p.Fail("reading transcript", err)
		return nil, false
	}
	return recs, true
}

// emitMeta emits the session_meta event before any other. The transcript
// carries no session ID or harness version (the conversation ID is the
// brain directory name), so the meta is sparse by design; a caller-supplied
// HarnessVersionHint is the only version source.
func (p *parser) emitMeta(rec *record) bool {
	p.meta = true
	return p.Emit(session.Event{
		Kind:       session.KindSessionMeta,
		Timestamp:  parseutil.ParseTime(rec.CreatedAt),
		Provenance: p.Prov(rec.raw),
		SessionMeta: &session.Meta{
			Harness:        string(harness.Antigravity),
			HarnessVersion: p.Opts.HarnessVersionHint,
		},
	})
}

func (p *parser) record(rec *record) bool {
	switch {
	case rec.parseErr != "":
		// Only reachable in permissive mode; strict failed in readAll.
		return p.Unknown("", rec.raw, rec.parseErr)
	case rec.StepIndex == nil:
		return p.UnknownOrFail(rec.Type, rec.raw, "record has no step_index")
	case rec.Type == "USER_INPUT":
		return p.userInput(rec)
	case rec.Type == "CONVERSATION_HISTORY":
		return p.system(rec, "conversation_history", "", "")
	case rec.Type == "SYSTEM_MESSAGE":
		return p.system(rec, "system_message", "", rec.Content)
	case rec.Type == "CHECKPOINT":
		return p.system(rec, "checkpoint", "", rec.Content)
	case rec.Type == "ERROR_MESSAGE":
		text := rec.Error
		if text == "" {
			text = rec.Content
		}
		return p.system(rec, "error_message", "error", text)
	case rec.Type == "PLANNER_RESPONSE":
		return p.planner(rec)
	case resultTypes[rec.Type]:
		return p.toolResult(rec)
	case ambiguousResultTypes[rec.Type]:
		if len(p.pending) > 0 {
			return p.toolResult(rec)
		}
		return p.system(rec, strings.ToLower(rec.Type), "", rec.Content)
	default:
		return p.UnknownOrFail(rec.Type, rec.raw, fmt.Sprintf("unrecognized record type %q", rec.Type))
	}
}

// userInput emits the human request as a user_message, and the harness
// wrapper (metadata, settings changes) as a separate system event so the
// two are never conflated. The model label is captured here: it is the
// only place the transcript names the model.
func (p *parser) userInput(rec *record) bool {
	if m := modelRe.FindStringSubmatch(rec.Content); m != nil {
		p.model = m[1]
	}
	request, remainder := splitUserInput(rec.Content)
	ev := session.Event{
		Kind:       session.KindUserMessage,
		Timestamp:  parseutil.ParseTime(rec.CreatedAt),
		Provenance: p.Prov(rec.raw),
		UserMessage: &session.UserMessage{
			Origin:  session.OriginHuman,
			Content: []session.ContentBlock{{Kind: session.ContentText, Text: request}},
		},
	}
	if !p.Emit(ev) {
		return false
	}
	if remainder == "" {
		return true
	}
	return p.system(rec, "user_input_context", "", remainder)
}

// planner emits an assistant turn: thinking (when present), the
// assistant_message anchor, then one tool_call per entry, all sharing a
// step-derived MessageID. Any still-pending calls are abandoned: a new
// planner round means their results will never arrive, and keeping them
// queued would misattribute later results.
func (p *parser) planner(rec *record) bool {
	p.pending = p.pending[:0]
	mid := fmt.Sprintf("step-%d", rec.step)
	ts := parseutil.ParseTime(rec.CreatedAt)

	if rec.Thinking != "" {
		ev := session.Event{
			Kind:       session.KindThinking,
			Timestamp:  ts,
			MessageID:  mid,
			Provenance: p.Prov(rec.raw),
			Thinking:   &session.Thinking{Text: rec.Thinking},
		}
		if !p.Emit(ev) {
			return false
		}
	}

	var content []session.ContentBlock
	if rec.Content != "" {
		content = []session.ContentBlock{{Kind: session.ContentText, Text: rec.Content}}
	}
	anchor := session.Event{
		Kind:       session.KindAssistantMessage,
		Timestamp:  ts,
		MessageID:  mid,
		Provenance: p.Prov(rec.raw),
		AssistantMessage: &session.AssistantMessage{
			Model:   p.model,
			Content: content,
		},
	}
	if !p.Emit(anchor) {
		return false
	}

	for i := range rec.ToolCalls {
		c := &rec.ToolCalls[i]
		id := fmt.Sprintf("step-%d:call-%d", rec.step, i)
		p.pending = append(p.pending, pendingCall{id: id, name: c.Name})
		ev := session.Event{
			Kind:       session.KindToolCall,
			Timestamp:  ts,
			MessageID:  mid,
			Provenance: p.Prov(rec.raw),
			ToolCall: &session.ToolCall{
				ToolCallID: id,
				Name:       c.Name,
				Kind:       kindFor(c.Name),
				Input:      c.Args,
			},
		}
		if !p.Emit(ev) {
			return false
		}
	}
	return true
}

func (p *parser) toolResult(rec *record) bool {
	var id, name string
	if len(p.pending) > 0 {
		pc := p.pending[0]
		p.pending = p.pending[1:]
		id, name = pc.id, pc.name
	} else {
		// No pending call: preserved as an orphan (counted in the report).
		id = fmt.Sprintf("step-%d:orphan", rec.step)
	}
	tr := &session.ToolResult{
		ToolCallID: id,
		ToolName:   name,
		IsError:    rec.Error != "",
		Content:    []session.ContentBlock{{Kind: session.ContentText, Text: rec.Content}},
	}
	parseutil.Truncate(tr, p.Opts.MaxPayloadBytes)
	return p.Emit(session.Event{
		Kind:       session.KindToolResult,
		Timestamp:  parseutil.ParseTime(rec.CreatedAt),
		Provenance: p.Prov(rec.raw),
		ToolResult: tr,
	})
}

func (p *parser) system(rec *record, subtype, level, text string) bool {
	return p.Emit(session.Event{
		Kind:       session.KindSystem,
		Timestamp:  parseutil.ParseTime(rec.CreatedAt),
		Provenance: p.Prov(rec.raw),
		System: &session.SystemEvent{
			Subtype: subtype,
			Level:   level,
			Text:    text,
			Details: rec.raw,
		},
	})
}

// splitUserInput separates the human request from the harness wrapper.
// USER_INPUT content wraps the prompt in <USER_REQUEST> tags followed by
// harness-injected metadata and settings-change blocks.
func splitUserInput(content string) (request, remainder string) {
	const open, closeTag = "<USER_REQUEST>", "</USER_REQUEST>"
	i := strings.Index(content, open)
	j := strings.Index(content, closeTag)
	if i < 0 || j < 0 || j < i {
		return strings.TrimSpace(content), ""
	}
	request = strings.TrimSpace(content[i+len(open) : j])
	remainder = strings.TrimSpace(content[:i] + content[j+len(closeTag):])
	return request, remainder
}
