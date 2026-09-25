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

	// ByAgent splits the session per agent when it holds more than one:
	// the parent under the empty key, each subagent under its agent id.
	// Set when events carry Event.AgentID (a harness that records
	// subagents inline) and by task-scope aggregation, where the keys are
	// the subagent transcripts' ids. Absent for a single-agent session,
	// whose top-level numbers are the whole story.
	ByAgent map[string]*AgentStats `json:"by_agent,omitempty" schema:"ext"`
}

// AgentStats is one agent's share of a session or task: the per-agent
// subset of Stats that is meaningful to split.
type AgentStats struct {
	Events            int `json:"events" schema:"ext"`
	AssistantMessages int `json:"assistant_messages" schema:"ext"`

	ToolCalls       int              `json:"tool_calls" schema:"ext"`
	ToolCallsByName map[string]int   `json:"tool_calls_by_name,omitempty" schema:"ext"`
	ToolCallsByKind map[ToolKind]int `json:"tool_calls_by_kind,omitempty" schema:"ext"`
	ToolErrors      int              `json:"tool_errors,omitempty" schema:"ext"`

	Models []string `json:"models,omitempty" schema:"otel"`

	// Totals is the agent's own token usage, nil when it recorded none.
	Totals *TokenUsage `json:"totals,omitempty" schema:"otel"`
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

	// Per-agent split, only when more than one agent wrote events.
	agents := map[string]*AgentStats{}
	agentOf := func(id string) *AgentStats {
		a := agents[id]
		if a == nil {
			a = &AgentStats{ToolCallsByName: map[string]int{}, ToolCallsByKind: map[ToolKind]int{}}
			agents[id] = a
		}
		return a
	}
	usageByAgent := map[string]*TokenUsage{}
	for i := range s.Events {
		ev := &s.Events[i]
		a := agentOf(ev.AgentID)
		a.Events++
		if ev.Kind == KindAssistantMessage {
			a.AssistantMessages++
			if m := ev.AssistantMessage.Model; m != "" && !slices.Contains(a.Models, m) {
				a.Models = append(a.Models, m)
			}
			if u := ev.AssistantMessage.Usage; u != nil {
				if usageByAgent[ev.AgentID] == nil {
					usageByAgent[ev.AgentID] = &TokenUsage{}
				}
				usageByAgent[ev.AgentID].Add(*u)
			}
		}
	}
	for id, u := range usageByAgent {
		u.TotalPromptTokens = TotalPromptTokens(s.Meta.Harness, u)
		agents[id].Totals = u
	}

	for _, ti := range s.ToolInteractions() {
		name := UnknownToolName
		if ti.Call != nil {
			call := ti.Call.ToolCall
			name = call.Name
			st.ToolCalls++
			st.ToolCallsByName[call.Name]++
			st.ToolCallsByKind[call.Kind]++
			a := agentOf(ti.Call.AgentID)
			a.ToolCalls++
			a.ToolCallsByName[call.Name]++
			a.ToolCallsByKind[call.Kind]++
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
				owner := rev.AgentID
				if ti.Call != nil {
					owner = ti.Call.AgentID
				}
				agentOf(owner).ToolErrors++
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
	if len(agents) > 1 {
		st.ByAgent = agents
	}
	return st
}

// AsAgent returns the whole session's numbers as one agent's share, for
// task-scope aggregation where each subagent transcript is one agent.
func (st *Stats) AsAgent() *AgentStats {
	a := &AgentStats{
		Events:            st.Events,
		AssistantMessages: st.EventCounts[KindAssistantMessage],
		ToolCalls:         st.ToolCalls,
		ToolCallsByName:   cloneMap(st.ToolCallsByName),
		ToolCallsByKind:   cloneMap(st.ToolCallsByKind),
		ToolErrors:        st.ToolErrors,
		Models:            slices.Clone(st.Models),
	}
	if st.Totals != nil {
		t := *st.Totals
		a.Totals = &t
	}
	return a
}

// SumStats aggregates the per-transcript summaries of one task (the
// parent and its subagent transcripts, all from the same harness) into a
// task-scope Stats: counts and per-name maps sum, models union in first-
// observed order, Totals sum when any part recorded usage (with
// TotalPromptTokens re-derived for the harness's convention, so a sum
// never mixes conventions), the time span covers every part, and
// FinalAnswer is the first part's (the parent's). ByAgent is left to the
// caller, which knows the parts' identities. Nothing is counted twice:
// each part's numbers come from its own transcript, and a harness whose
// parent already includes its subagents' events (Copilot) contributes a
// single part.
func SumStats(harness string, parts ...*Stats) *Stats {
	out := &Stats{
		EventCounts:       map[EventKind]int{},
		ToolCallsByName:   map[string]int{},
		ToolCallsByKind:   map[ToolKind]int{},
		ResultBytesByName: map[string]int64{},
		ToolTimeMSByName:  map[string]int64{},
		SystemBySubtype:   map[string]int{},
	}
	var totals *TokenUsage
	for _, p := range parts {
		if p == nil {
			continue
		}
		out.Events += p.Events
		addMap(out.EventCounts, p.EventCounts)
		out.UserMessages += p.UserMessages
		out.HarnessMessages += p.HarnessMessages
		out.ToolCalls += p.ToolCalls
		addMap(out.ToolCallsByName, p.ToolCallsByName)
		addMap(out.ToolCallsByKind, p.ToolCallsByKind)
		out.ToolErrors += p.ToolErrors
		out.UnansweredCalls += p.UnansweredCalls
		out.OrphanResults += p.OrphanResults
		out.ResultBytes += p.ResultBytes
		addMap(out.ResultBytesByName, p.ResultBytesByName)
		out.FetchRawBytes += p.FetchRawBytes
		addMap(out.ToolTimeMSByName, p.ToolTimeMSByName)
		addMap(out.SystemBySubtype, p.SystemBySubtype)
		for _, m := range p.Models {
			if !slices.Contains(out.Models, m) {
				out.Models = append(out.Models, m)
			}
		}
		if p.Totals != nil {
			if totals == nil {
				totals = &TokenUsage{}
			}
			totals.Add(*p.Totals)
		}
		if p.StartTime != nil && (out.StartTime == nil || p.StartTime.Before(*out.StartTime)) {
			t := *p.StartTime
			out.StartTime = &t
		}
		if p.EndTime != nil && (out.EndTime == nil || p.EndTime.After(*out.EndTime)) {
			t := *p.EndTime
			out.EndTime = &t
		}
	}
	if len(parts) > 0 && parts[0] != nil {
		out.FinalAnswer = parts[0].FinalAnswer
	}
	if totals != nil {
		totals.TotalPromptTokens = totalPromptTokens(harness, totals)
		out.Totals = totals
	}
	if out.StartTime != nil && out.EndTime != nil {
		out.WallTimeMS = out.EndTime.Sub(*out.StartTime).Milliseconds()
	}
	return out
}

// Add accumulates v's share into a (task-scope merge of one agent that
// appears in several parts). Totals sum without re-deriving
// TotalPromptTokens; callers re-derive with TotalPromptTokens.
func (a *AgentStats) Add(v *AgentStats) {
	a.Events += v.Events
	a.AssistantMessages += v.AssistantMessages
	a.ToolCalls += v.ToolCalls
	if a.ToolCallsByName == nil {
		a.ToolCallsByName = map[string]int{}
	}
	if a.ToolCallsByKind == nil {
		a.ToolCallsByKind = map[ToolKind]int{}
	}
	addMap(a.ToolCallsByName, v.ToolCallsByName)
	addMap(a.ToolCallsByKind, v.ToolCallsByKind)
	a.ToolErrors += v.ToolErrors
	for _, m := range v.Models {
		if !slices.Contains(a.Models, m) {
			a.Models = append(a.Models, m)
		}
	}
	if v.Totals != nil {
		if a.Totals == nil {
			a.Totals = &TokenUsage{}
		}
		a.Totals.Add(*v.Totals)
	}
}

func addMap[K comparable, V int | int64](dst, src map[K]V) {
	for k, v := range src {
		dst[k] += v
	}
}

func cloneMap[K comparable, V any](m map[K]V) map[K]V {
	if m == nil {
		return nil
	}
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
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
