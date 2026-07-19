package session

import (
	"slices"
	"time"
)

// UnknownToolName is the key under which per-tool Stats maps (such as
// ResultBytesByName and ToolTimeMSByName) bucket results that could not be
// attributed to a named tool: orphan results whose payload carries no tool
// name. The literal appears in serialized Stats JSON and is part of the
// output contract.
const UnknownToolName = "(unknown)"

// Stats is a behavioral summary of one session: tool selection, retrieval
// volume, timing, and token totals. It answers "how did the agent behave"
// questions (which tools, how many calls, how many bytes came back, how
// long things took) without consumers re-walking the event stream.
type Stats struct {
	Events      int               `json:"events" schema:"ext"`
	EventCounts map[EventKind]int `json:"event_counts,omitempty" schema:"ext"`

	// UserMessages counts human-origin messages; HarnessMessages counts
	// harness-injected user-role content.
	UserMessages    int `json:"user_messages" schema:"ext"`
	HarnessMessages int `json:"harness_messages,omitempty" schema:"ext"`

	ToolCalls       int              `json:"tool_calls" schema:"ext"`
	ToolCallsByName map[string]int   `json:"tool_calls_by_name,omitempty" schema:"ext"`
	ToolCallsByKind map[ToolKind]int `json:"tool_calls_by_kind,omitempty" schema:"ext"`

	// ToolErrors counts results flagged as errors. UnansweredCalls are
	// calls with no observed result; OrphanResults are results with no
	// observed call.
	ToolErrors      int `json:"tool_errors,omitempty" schema:"ext"`
	UnansweredCalls int `json:"unanswered_calls,omitempty" schema:"ext"`
	OrphanResults   int `json:"orphan_results,omitempty" schema:"ext"`

	// ResultBytes measures tool-result content as the model received it
	// (truncated payloads count their original size). FetchRawBytes sums
	// pre-pipeline fetch sizes where the harness reported them; comparing
	// the two exposes fetch-pipeline compression. Bytes from results with
	// no attributable tool name are keyed under UnknownToolName.
	ResultBytes       int64            `json:"result_bytes" schema:"ext"`
	ResultBytesByName map[string]int64 `json:"result_bytes_by_name,omitempty" schema:"ext"`
	FetchRawBytes     int64            `json:"fetch_raw_bytes,omitempty" schema:"ext"`

	// ToolTimeMSByName sums call-to-last-result latency per tool name,
	// where both timestamps exist.
	ToolTimeMSByName map[string]int64 `json:"tool_time_ms_by_name,omitempty" schema:"ext"`

	// SystemBySubtype counts system events per subtype. Actions a harness
	// records only as telemetry live here until promoted (e.g. Codex
	// 0.144's web_search_end), so a consumer can see retrieval attempts
	// that produced no tool_call and know to opt into a promotion.
	SystemBySubtype map[string]int `json:"system_by_subtype,omitempty" schema:"ext"`

	// Models lists the models observed on assistant messages, in
	// first-observed order. More than one entry means the serving model
	// changed mid-session (e.g. a fallback) — an experimental confound
	// worth flagging. Empty when the format records no model.
	Models []string `json:"models,omitempty" schema:"otel"`

	// Totals is nil when the transcript recorded no usage at all (e.g.
	// Antigravity), which is not the same claim as a measured zero.
	Totals *TokenUsage `json:"totals,omitempty" schema:"otel"`

	// StartTime/EndTime are the min/max event timestamps (not first/last
	// event: harnesses can record events out of time order).
	StartTime  *time.Time `json:"start_time,omitempty" schema:"ext"`
	EndTime    *time.Time `json:"end_time,omitempty" schema:"ext"`
	WallTimeMS int64      `json:"wall_time_ms,omitempty" schema:"ext"`

	// FinalAnswer is the text of the last assistant message with content,
	// in stream order (assistant messages are causally ordered in-stream
	// even when their timestamps are not; contrast StartTime/EndTime).
	FinalAnswer string `json:"final_answer,omitempty" schema:"ext"`
}

// Stats computes the session's behavioral summary.
func (s *Session) Stats() *Stats {
	st := &Stats{
		Events:            len(s.Events),
		EventCounts:       make(map[EventKind]int),
		ToolCallsByName:   make(map[string]int),
		ToolCallsByKind:   make(map[ToolKind]int),
		ResultBytesByName: make(map[string]int64),
		ToolTimeMSByName:  make(map[string]int64),
		SystemBySubtype:   make(map[string]int),
	}
	if s.Totals != nil {
		t := *s.Totals
		st.Totals = &t
	}

	for i := range s.Events {
		ev := &s.Events[i]
		st.EventCounts[ev.Kind]++
		if ts := ev.Timestamp; ts != nil {
			if st.StartTime == nil || ts.Before(*st.StartTime) {
				t := *ts
				st.StartTime = &t
			}
			if st.EndTime == nil || ts.After(*st.EndTime) {
				t := *ts
				st.EndTime = &t
			}
		}
		switch ev.Kind {
		case KindUserMessage:
			if ev.UserMessage.Origin == OriginHarness {
				st.HarnessMessages++
			} else {
				st.UserMessages++
			}
		case KindAssistantMessage:
			if text := ev.AssistantMessage.Text(); text != "" {
				st.FinalAnswer = text
			}
			if m := ev.AssistantMessage.Model; m != "" && !slices.Contains(st.Models, m) {
				st.Models = append(st.Models, m)
			}
		case KindSystem:
			st.SystemBySubtype[ev.System.Subtype]++
		}
	}
	if st.StartTime != nil && st.EndTime != nil {
		st.WallTimeMS = st.EndTime.Sub(*st.StartTime).Milliseconds()
	}

	for _, ti := range s.ToolInteractions() {
		name := UnknownToolName
		if ti.Call != nil {
			call := ti.Call.ToolCall
			name = call.Name
			st.ToolCalls++
			st.ToolCallsByName[call.Name]++
			st.ToolCallsByKind[call.Kind]++
			if len(ti.Results) == 0 {
				st.UnansweredCalls++
			}
		} else {
			st.OrphanResults += len(ti.Results)
			if len(ti.Results) > 0 && ti.Results[0].ToolResult.ToolName != "" {
				name = ti.Results[0].ToolResult.ToolName
			}
		}

		var lastResult *time.Time
		for _, rev := range ti.Results {
			tr := rev.ToolResult
			if tr.IsError {
				st.ToolErrors++
			}
			b := resultBytes(tr.Content)
			st.ResultBytes += b
			st.ResultBytesByName[name] += b
			if tr.Fetch != nil {
				st.FetchRawBytes += tr.Fetch.RawBytes
			}
			if rev.Timestamp != nil && (lastResult == nil || rev.Timestamp.After(*lastResult)) {
				lastResult = rev.Timestamp
			}
		}
		if ti.Call != nil && ti.Call.Timestamp != nil && lastResult != nil {
			if d := lastResult.Sub(*ti.Call.Timestamp); d >= 0 {
				st.ToolTimeMSByName[name] += d.Milliseconds()
			}
		}
	}
	return st
}

// resultBytes measures result content as delivered to the model: truncated
// blocks count their recorded original size.
func resultBytes(blocks []ContentBlock) int64 {
	var n int64
	for i := range blocks {
		b := &blocks[i]
		if b.Truncated {
			n += b.Size
			continue
		}
		n += int64(len(b.Text)) + int64(len(b.Raw))
	}
	return n
}
