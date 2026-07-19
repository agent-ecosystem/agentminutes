package session

import "fmt"

// Session is a fully accumulated session record: identity, ordered events,
// and end-of-stream aggregates. It is what whole-file parsing returns and
// what the CLI serializes in whole-session mode.
type Session struct {
	// SchemaVersion is the normalized-schema revision that produced this
	// record.
	SchemaVersion string `json:"agentminutes_schema" schema:"ext"`

	Meta   Meta    `json:"meta" schema:"ext"`
	Events []Event `json:"events" schema:"ext"`

	// Totals sums token usage across the session's assistant messages. Nil
	// when the transcript recorded no usage at all (e.g. Antigravity),
	// which is not the same claim as a measured zero.
	Totals *TokenUsage `json:"totals,omitempty" schema:"otel"`

	Report Report `json:"report" schema:"ext"`
}

// Report accounts for everything that did not become a normal event, so
// consumers can verify nothing was silently dropped.
type Report struct {
	// SkippedRecords counts native records excluded by the adapter's
	// documented skip list, keyed by native record type.
	SkippedRecords map[string]int `json:"skipped_records,omitempty" schema:"ext"`

	// UnknownEvents counts records preserved as KindUnknown events
	// (permissive mode only).
	UnknownEvents int `json:"unknown_events,omitempty" schema:"ext"`

	// OrphanToolResults counts tool results whose ToolCallID matched no
	// observed tool call.
	OrphanToolResults int `json:"orphan_tool_results,omitempty" schema:"ext"`
}

// ToolInteraction joins a tool call with its observed results. Call is nil
// for orphaned results; Results is empty for calls that never produced one
// (e.g. the session ended first).
type ToolInteraction struct {
	Call    *Event   `json:"call,omitempty" schema:"ext"`
	Results []*Event `json:"results,omitempty" schema:"ext"`
}

// ToolInteractions joins the session's tool calls with their results, in
// call order. Orphaned results (no matching call) are appended at the end
// in result order. Correlation assumes unique ToolCallIDs (Event.Validate
// requires them non-empty); if a session nevertheless repeats an ID, results
// attach to the most recent call bearing it.
func (s *Session) ToolInteractions() []ToolInteraction {
	var interactions []ToolInteraction
	byCallID := make(map[string]int)
	for i := range s.Events {
		ev := &s.Events[i]
		switch ev.Kind {
		case KindToolCall:
			byCallID[ev.ToolCall.ToolCallID] = len(interactions)
			interactions = append(interactions, ToolInteraction{Call: ev})
		case KindToolResult:
			if idx, ok := byCallID[ev.ToolResult.ToolCallID]; ok {
				interactions[idx].Results = append(interactions[idx].Results, ev)
			} else {
				interactions = append(interactions, ToolInteraction{Results: []*Event{ev}})
			}
		}
	}
	return interactions
}

// Accumulator builds a Session from a stream of events. It is the bridge
// between streaming and whole-file parsing: adapters and streaming
// consumers feed it events; Session returns the accumulated record.
//
// Adapters emit a KindSessionMeta event first (see the harness package's
// adapter contract). A session accumulated without one serializes an empty
// Meta, including an empty required "harness" field.
type Accumulator struct {
	meta      *Meta
	events    []Event
	totals    TokenUsage
	usageSeen bool
	unknown   int
	skipped   map[string]int
}

// NewAccumulator returns an empty Accumulator.
func NewAccumulator() *Accumulator {
	return &Accumulator{skipped: make(map[string]int)}
}

// Add validates and appends an event, updating aggregates.
func (a *Accumulator) Add(ev Event) error {
	if err := ev.Validate(); err != nil {
		return err
	}
	switch ev.Kind {
	case KindSessionMeta:
		if a.meta != nil {
			return fmt.Errorf("session: duplicate %s event", KindSessionMeta)
		}
		m := *ev.SessionMeta
		a.meta = &m
	case KindAssistantMessage:
		if ev.AssistantMessage.Usage != nil {
			a.usageSeen = true
			a.totals.Add(*ev.AssistantMessage.Usage)
		}
	case KindUnknown:
		a.unknown++
	}
	a.events = append(a.events, ev)
	return nil
}

// SkipRecord counts a native record the adapter excluded per its documented
// skip list.
func (a *Accumulator) SkipRecord(recordType string) {
	if a.skipped == nil {
		a.skipped = make(map[string]int)
	}
	a.skipped[recordType]++
}

// Session returns the accumulated record. The Accumulator must not be used
// afterward.
func (a *Accumulator) Session() *Session {
	s := &Session{
		SchemaVersion: SchemaVersion,
		Events:        a.events,
		Report: Report{
			UnknownEvents: a.unknown,
		},
	}
	if a.usageSeen {
		t := a.totals
		s.Totals = &t
	}
	if a.meta != nil {
		s.Meta = *a.meta
	}
	if len(a.skipped) > 0 {
		s.Report.SkippedRecords = a.skipped
	}
	for _, ti := range s.ToolInteractions() {
		if ti.Call == nil {
			s.Report.OrphanToolResults += len(ti.Results)
		}
	}
	// Detach internal state so later Accumulator use cannot mutate the
	// returned record through the shared slice and map.
	a.meta = nil
	a.events = nil
	a.skipped = nil
	return s
}
