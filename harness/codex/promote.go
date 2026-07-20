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
	return promoteTelemetry(events, "web_search_end", promoteWebSearchEnd)
}

// promotedPatchApply names the native source on events synthesized by
// PromotePatchApply.
const promotedPatchApply = "event_msg/patch_apply_end"

// PromotePatchApply is a session.Transform that replaces event_msg
// patch_apply_end system events with a synthesized edit tool_call +
// tool_result pair (name "apply_patch", kind edit, the changes map as
// input, payload in enrichment, is_error when the telemetry reports
// failure). Observed via drift probe on Codex 0.144.6: apply_patch rides
// inside the unified exec tool (a custom_tool_call named "exec" whose
// input is a script calling tools.apply_patch), so this telemetry is the
// only structured record of a file edit and without the transform edits
// are invisible to tool metrics — the same shape the fetch side reached
// at 0.144.1 (see PromoteWebSearch).
//
// It is never applied by default: harness versions that record apply_patch
// as its own tool call would be double-counted. The synthesized events
// carry PromotedFrom so consumers can audit or dedupe. Compose it
// explicitly:
//
//	s, err := harness.Parse(codex.Adapter{}, r, opts, codex.PromotePatchApply)
//
// Other harnesses' streams never carry this subtype, so the transform is a
// no-op on them. Design record: plans/telemetry-promotion.md.
func PromotePatchApply(events iter.Seq2[session.Event, error]) iter.Seq2[session.Event, error] {
	return promoteTelemetry(events, "patch_apply_end", promotePatchApplyEnd)
}

// promoteTelemetry is the shared promotion loop: system events with the
// given subtype are replaced by the synthesized pair; everything else
// (including errors) passes through untouched.
func promoteTelemetry(events iter.Seq2[session.Event, error], subtype string, synth func(*session.Event) (call, result session.Event)) iter.Seq2[session.Event, error] {
	return func(yield func(session.Event, error) bool) {
		for ev, err := range events {
			if err != nil || ev.Kind != session.KindSystem || ev.System == nil || ev.System.Subtype != subtype {
				if !yield(ev, err) {
					return
				}
				continue
			}
			call, result := synth(&ev)
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

func promotePatchApplyEnd(ev *session.Event) (call, result session.Event) {
	var pl struct {
		CallID  string          `json:"call_id"`
		Changes json.RawMessage `json:"changes"`
		Success *bool           `json:"success"`
	}
	// Details is the verbatim payload the adapter preserved; fields the
	// telemetry lacks simply stay zero.
	_ = json.Unmarshal(ev.System.Details, &pl)
	id := pl.CallID
	if id == "" && ev.Provenance != nil {
		id = fmt.Sprintf("patch_apply_end:L%d", ev.Provenance.Line)
	}
	call = session.Event{
		Kind:       session.KindToolCall,
		Timestamp:  ev.Timestamp,
		MessageID:  ev.MessageID,
		Provenance: ev.Provenance,
		ToolCall: &session.ToolCall{
			ToolCallID:   id,
			Name:         "apply_patch",
			Kind:         session.ToolKindEdit,
			Input:        pl.Changes,
			PromotedFrom: promotedPatchApply,
		},
	}
	result = session.Event{
		Kind:       session.KindToolResult,
		Timestamp:  ev.Timestamp,
		MessageID:  ev.MessageID,
		Provenance: ev.Provenance,
		ToolResult: &session.ToolResult{
			ToolCallID:   id,
			ToolName:     "apply_patch",
			IsError:      pl.Success != nil && !*pl.Success,
			Enrichment:   ev.System.Details,
			PromotedFrom: promotedPatchApply,
		},
	}
	return call, result
}
