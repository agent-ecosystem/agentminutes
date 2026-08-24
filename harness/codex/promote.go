package codex

import (
	"encoding/json"
	"fmt"
	"iter"

	"github.com/agent-ecosystem/agentminutes/session"
)

// promotedWebSearch names the native source on events synthesized by
// PromoteWebSearch from the pre-0.149 telemetry shape.
const promotedWebSearch = "event_msg/web_search_end"

// promotedWebSearchItem names the native source on events synthesized by
// PromoteWebSearch from the 0.149+ item stream.
const promotedWebSearchItem = "event_msg/item_completed/Extension"

// PromoteWebSearch is a session.Transform that replaces the system events
// recording a web retrieval with a synthesized fetch tool_call +
// tool_result pair, mirroring the adapter's handling of the 0.118
// response_item web_search_call (name "web_search", kind fetch, action as
// input, payload in enrichment). It recognizes both telemetry shapes:
// event_msg web_search_end (0.144 through 0.146) and, since 0.149, the
// event_msg item_completed record whose item is an Extension of kind
// "web.search" — the web_search_end subtype is gone from 0.149.1
// transcripts, so the item is the only trace of a URL fetch there. Without
// the transform retrieval is invisible to tool metrics on all of these
// versions.
//
// It is never applied by default: harness versions that also record the
// action as a response_item would be double-counted. The synthesized
// events carry PromotedFrom so consumers can audit or dedupe. Compose it
// explicitly:
//
//	s, err := harness.Parse(codex.Adapter{}, r, opts, codex.PromoteWebSearch)
//
// Other harnesses' streams never carry these subtypes, so the transform is
// a no-op on them. Design record: plans/telemetry-promotion.md.
func PromoteWebSearch(events iter.Seq2[session.Event, error]) iter.Seq2[session.Event, error] {
	return promoteTelemetry(events, func(ev *session.Event) (call, result session.Event, ok bool) {
		switch ev.System.Subtype {
		case "web_search_end":
			call, result = promoteWebSearchEnd(ev)
			return call, result, true
		case "item_completed":
			if item := itemOf(ev); item.Type == "Extension" && item.Kind == "web.search" {
				call, result = promoteWebSearchItem(ev, item)
				return call, result, true
			}
		}
		return call, result, false
	})
}

// promotedPatchApply names the native source on events synthesized by
// PromotePatchApply from the pre-0.149 telemetry shape.
const promotedPatchApply = "event_msg/patch_apply_end"

// promotedFileChangeItem names the native source on events synthesized by
// PromotePatchApply from the 0.149+ item stream.
const promotedFileChangeItem = "event_msg/item_completed/FileChange"

// PromotePatchApply is a session.Transform that replaces the system events
// recording a file edit with a synthesized edit tool_call + tool_result
// pair (name "apply_patch", kind edit, the changes map as input, payload
// in enrichment, is_error when the telemetry reports failure). Since
// 0.144.6 apply_patch rides inside the unified exec tool (a
// custom_tool_call named "exec" whose input is a script calling
// tools.apply_patch), so telemetry is the only structured record of a file
// edit. The transform recognizes both telemetry shapes: event_msg
// patch_apply_end (0.144 through 0.146) and, since 0.149, the event_msg
// item_completed record whose item is a FileChange — the patch_apply_end
// subtype is gone from 0.149.1 transcripts. Without the transform edits
// are invisible to tool metrics on all of these versions — the same shape
// the fetch side reached at 0.144.1 (see PromoteWebSearch).
//
// It is never applied by default: harness versions that record apply_patch
// as its own tool call would be double-counted. The synthesized events
// carry PromotedFrom so consumers can audit or dedupe. Compose it
// explicitly:
//
//	s, err := harness.Parse(codex.Adapter{}, r, opts, codex.PromotePatchApply)
//
// Other harnesses' streams never carry these subtypes, so the transform is
// a no-op on them. Design record: plans/telemetry-promotion.md.
func PromotePatchApply(events iter.Seq2[session.Event, error]) iter.Seq2[session.Event, error] {
	return promoteTelemetry(events, func(ev *session.Event) (call, result session.Event, ok bool) {
		switch ev.System.Subtype {
		case "patch_apply_end":
			call, result = promotePatchApplyEnd(ev)
			return call, result, true
		case "item_completed":
			if item := itemOf(ev); item.Type == "FileChange" {
				call, result = promoteFileChangeItem(ev, item)
				return call, result, true
			}
		}
		return call, result, false
	})
}

// promoteTelemetry is the shared promotion loop: system events the synth
// function claims (ok) are replaced by the synthesized pair; everything
// else (including errors) passes through untouched.
func promoteTelemetry(events iter.Seq2[session.Event, error], synth func(*session.Event) (call, result session.Event, ok bool)) iter.Seq2[session.Event, error] {
	return func(yield func(session.Event, error) bool) {
		for ev, err := range events {
			if err == nil && ev.Kind == session.KindSystem && ev.System != nil {
				if call, result, ok := synth(&ev); ok {
					if !yield(call, nil) || !yield(result, nil) {
						return
					}
					continue
				}
			}
			if !yield(ev, err) {
				return
			}
		}
	}
}

// completedItem is the item object inside an item_completed payload
// (0.149+); unused fields stay zero for any given item type.
type completedItem struct {
	Type    string          `json:"type"`
	Kind    string          `json:"kind"` // Extension: e.g. "web.search"
	ID      string          `json:"id"`
	Status  string          `json:"status"`
	Changes json.RawMessage `json:"changes"` // FileChange
	Action  json.RawMessage `json:"action"`  // Extension
}

// itemOf extracts the typed item from an item_completed system event's
// preserved payload; a malformed payload yields the zero item, which
// matches no promotion.
func itemOf(ev *session.Event) completedItem {
	var pl struct {
		Item completedItem `json:"item"`
	}
	_ = json.Unmarshal(ev.System.Details, &pl)
	return pl.Item
}

// itemID returns the item's own ID, or a line-derived fallback.
func itemID(ev *session.Event, item *completedItem) string {
	if item.ID != "" {
		return item.ID
	}
	if ev.Provenance != nil {
		return fmt.Sprintf("item_completed:L%d", ev.Provenance.Line)
	}
	return ""
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
	return fetchPair(ev, id, pl.Action, false, promotedWebSearch)
}

func promoteWebSearchItem(ev *session.Event, item completedItem) (call, result session.Event) {
	// The Extension item carries no success signal; a failed retrieval
	// surfaces in the exec tool's own output, not here.
	return fetchPair(ev, itemID(ev, &item), item.Action, false, promotedWebSearchItem)
}

func fetchPair(ev *session.Event, id string, action json.RawMessage, isError bool, marker string) (call, result session.Event) {
	call = session.Event{
		Kind:       session.KindToolCall,
		Timestamp:  ev.Timestamp,
		MessageID:  ev.MessageID,
		Provenance: ev.Provenance,
		ToolCall: &session.ToolCall{
			ToolCallID:   id,
			Name:         "web_search",
			Kind:         session.ToolKindFetch,
			Input:        action,
			PromotedFrom: marker,
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
			IsError:      isError,
			Enrichment:   ev.System.Details,
			PromotedFrom: marker,
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
	return editPair(ev, id, pl.Changes, pl.Success != nil && !*pl.Success, promotedPatchApply)
}

func promoteFileChangeItem(ev *session.Event, item completedItem) (call, result session.Event) {
	isError := item.Status != "" && item.Status != "completed"
	return editPair(ev, itemID(ev, &item), item.Changes, isError, promotedFileChangeItem)
}

func editPair(ev *session.Event, id string, changes json.RawMessage, isError bool, marker string) (call, result session.Event) {
	call = session.Event{
		Kind:       session.KindToolCall,
		Timestamp:  ev.Timestamp,
		MessageID:  ev.MessageID,
		Provenance: ev.Provenance,
		ToolCall: &session.ToolCall{
			ToolCallID:   id,
			Name:         "apply_patch",
			Kind:         session.ToolKindEdit,
			Input:        changes,
			PromotedFrom: marker,
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
			IsError:      isError,
			Enrichment:   ev.System.Details,
			PromotedFrom: marker,
		},
	}
	return call, result
}
