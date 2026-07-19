package codex

import (
	"encoding/json"
	"fmt"
	"iter"

	"github.com/agent-ecosystem/agentminutes/session"
)

// promotedWebSearch names the native source on events synthesized by
// PromoteWebSearch.
const promotedWebSearch = "event_msg/web_search_end"

// PromoteWebSearch is a session.Transform that replaces event_msg
// web_search_end system events with a synthesized fetch tool_call +
// tool_result pair, mirroring the adapter's handling of the 0.118
// response_item web_search_call (name "web_search", kind fetch, action as
// input, payload in enrichment). On Codex 0.144+ this telemetry is the
// only trace of a URL fetch, so without the transform retrieval is
// invisible to tool metrics.
//
// It is never applied by default: harness versions that also record the
// action as a response_item would be double-counted. The synthesized
// events carry PromotedFrom so consumers can audit or dedupe. Compose it
// explicitly:
//
//	s, err := harness.Parse(codex.Adapter{}, r, opts, codex.PromoteWebSearch)
//
// Other harnesses' streams never carry this subtype, so the transform is a
// no-op on them. Design record: plans/telemetry-promotion.md.
func PromoteWebSearch(events iter.Seq2[session.Event, error]) iter.Seq2[session.Event, error] {
	return func(yield func(session.Event, error) bool) {
		for ev, err := range events {
			if err != nil || ev.Kind != session.KindSystem || ev.System == nil || ev.System.Subtype != "web_search_end" {
				if !yield(ev, err) {
					return
				}
				continue
			}
			call, result := promoteWebSearchEnd(&ev)
			if !yield(call, nil) || !yield(result, nil) {
				return
			}
		}
	}
}

func promoteWebSearchEnd(ev *session.Event) (call, result session.Event) {
	var pl struct {
		CallID string          `json:"call_id"`
		Action json.RawMessage `json:"action"`
	}
	// Details is the verbatim payload the adapter preserved; fields the
	// telemetry lacks simply stay zero.
	_ = json.Unmarshal(ev.System.Details, &pl)
	id := pl.CallID
	if id == "" && ev.Provenance != nil {
		id = fmt.Sprintf("web_search_end:L%d", ev.Provenance.Line)
	}
	call = session.Event{
		Kind:       session.KindToolCall,
		Timestamp:  ev.Timestamp,
		MessageID:  ev.MessageID,
		Provenance: ev.Provenance,
		ToolCall: &session.ToolCall{
			ToolCallID:   id,
			Name:         "web_search",
			Kind:         session.ToolKindFetch,
			Input:        pl.Action,
			PromotedFrom: promotedWebSearch,
		},
	}
	result = session.Event{
		Kind:       session.KindToolResult,
		Timestamp:  ev.Timestamp,
		MessageID:  ev.MessageID,
		Provenance: ev.Provenance,
		ToolResult: &session.ToolResult{
			ToolCallID:   id,
			ToolName:     "web_search",
			Enrichment:   ev.System.Details,
			PromotedFrom: promotedWebSearch,
		},
	}
	return call, result
}
