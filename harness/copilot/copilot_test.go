package copilot

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/parseutil"
	"github.com/agent-ecosystem/agentminutes/internal/textaudit"
	"github.com/agent-ecosystem/agentminutes/session"
)

func fixtureBytes(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func parseFixture(t *testing.T, opts harness.Options) *session.Session {
	t.Helper()
	s, err := harness.Parse(Adapter{}, bytes.NewReader(fixtureBytes(t)), opts)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// startLine renders a minimal session.start record.
func startLine(sessionID, cwd, ts string) string {
	return `{"type":"session.start","data":{"sessionId":"` + sessionID + `","version":1,"producer":"copilot-agent","copilotVersion":"1.0.88","startTime":"` + ts + `","context":{"cwd":"` + cwd + `"}},"id":"e-1","timestamp":"` + ts + `","parentId":null}` + "\n"
}

func TestFixtureEventSequence(t *testing.T) {
	s := parseFixture(t, harness.Options{})

	want := []session.EventKind{
		session.KindSessionMeta,      // L1 session.start
		session.KindSystem,           // L2 session.permissions_changed
		session.KindSystem,           // L3 session.model_change
		session.KindUserMessage,      // L4
		session.KindSystem,           // L5 system.message
		session.KindSystem,           // L6 assistant.turn_start
		session.KindThinking,         // L7 reasoning block
		session.KindToolCall,         // L7 call-1 bash
		session.KindAssistantMessage, // L7 anchor
		session.KindSystem,           // L8 tool.execution_start
		session.KindToolResult,       // L9 call-1
		session.KindSystem,           // L10 assistant.turn_end
		session.KindSystem,           // L11 assistant.turn_start
		session.KindToolCall,         // L12 call-2 create
		session.KindAssistantMessage, // L12 anchor
		session.KindSystem,           // L13 tool.execution_start
		session.KindSystem,           // L14 permission.requested
		session.KindSystem,           // L15 permission.completed
		session.KindToolResult,       // L16 call-2 denied
		session.KindSystem,           // L17 assistant.turn_end
		session.KindSystem,           // L18 assistant.turn_start
		session.KindAssistantMessage, // L19 "hello"
		session.KindSystem,           // L20 assistant.turn_end
		session.KindSystem,           // L21 session.usage_checkpoint
		session.KindSystem,           // L22 session.shutdown
		session.KindSystem,           // L23 session.resume
		session.KindSystem,           // L24 session.permissions_changed
		session.KindUserMessage,      // L25
		session.KindSystem,           // L26 assistant.turn_start
		session.KindToolCall,         // L27 call-3
		session.KindToolCall,         // L27 call-4 (parallel)
		session.KindAssistantMessage, // L27 anchor
		session.KindSystem,           // L28 tool.execution_start
		session.KindToolResult,       // L29 call-3
		session.KindSystem,           // L30 tool.execution_start (orphan)
		session.KindToolResult,       // L31 orphan result
		session.KindSystem,           // L32 abort
		session.KindSystem,           // L33 session.info
		session.KindSystem,           // L34 session.shutdown
	}
	got := make([]session.EventKind, len(s.Events))
	for i, ev := range s.Events {
		got[i] = ev.Kind
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events %v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d: kind %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFixtureMetaTotalsReport(t *testing.T) {
	s := parseFixture(t, harness.Options{})

	m := s.Meta
	if m.Harness != "copilot" || m.HarnessVersion != "1.0.88" ||
		m.SessionID != "sess-copilot-1" || m.CWD != "/tmp/exp" || m.GitBranch != "main" {
		t.Errorf("Meta = %+v", m)
	}
	if s.Events[0].Timestamp == nil || s.Events[0].ID != "e-01" || s.Events[0].ParentID != "" {
		t.Errorf("meta event = %+v", s.Events[0])
	}

	// The format records no per-message usage; totals must be absent
	// (not zero). The session-cumulative numbers ride in the
	// session.shutdown system event.
	if s.Totals != nil {
		t.Errorf("Totals = %+v, want nil", s.Totals)
	}
	if len(s.Report.SkippedRecords) != 0 {
		t.Errorf("SkippedRecords = %v, want none (no skip list)", s.Report.SkippedRecords)
	}
	if s.Report.OrphanToolResults != 1 {
		t.Errorf("OrphanToolResults = %d, want 1", s.Report.OrphanToolResults)
	}
	if s.Report.UnknownEvents != 0 {
		t.Errorf("UnknownEvents = %d, want 0", s.Report.UnknownEvents)
	}
}

func TestFixtureAssistantAnchors(t *testing.T) {
	s := parseFixture(t, harness.Options{})

	var anchors []*session.Event
	for i := range s.Events {
		if s.Events[i].Kind == session.KindAssistantMessage {
			anchors = append(anchors, &s.Events[i])
		}
	}
	if len(anchors) != 4 {
		t.Fatalf("got %d assistant_message events, want 4 (one per API message)", len(anchors))
	}
	first := anchors[0]
	if first.MessageID != "am-1" || first.ID != "e-07" || first.ParentID != "e-06" || first.Provenance.Line != 7 {
		t.Errorf("anchor 1 = %+v", first)
	}
	am := first.AssistantMessage
	if am.Model != "claude-sonnet-5" || len(am.Content) != 0 || am.Usage != nil {
		t.Errorf("anchor 1 payload = %+v, want model, no content, nil usage", am)
	}
	if text := anchors[2].AssistantMessage.Text(); text != "hello" {
		t.Errorf("anchor 3 text = %q, want hello", text)
	}

	// Thinking and tool calls share the anchor's message id.
	for _, ev := range s.Events {
		if (ev.Kind == session.KindThinking || ev.Kind == session.KindToolCall) && ev.Provenance.Line == 7 && ev.MessageID != "am-1" {
			t.Errorf("%s at line 7 has message id %q, want am-1", ev.Kind, ev.MessageID)
		}
	}
}

func TestFixtureThinking(t *testing.T) {
	s := parseFixture(t, harness.Options{})
	var thinking []*session.Thinking
	for _, ev := range s.Events {
		if ev.Kind == session.KindThinking {
			thinking = append(thinking, ev.Thinking)
		}
	}
	if len(thinking) != 1 {
		t.Fatalf("got %d thinking events, want 1", len(thinking))
	}
	// The provider-native block is verbatim (trailing newlines kept),
	// not the harness's trimmed reasoningText.
	if thinking[0].Text != "Read the note first.\n\n" || thinking[0].Signature != "SIG1" {
		t.Errorf("thinking = %+v", thinking[0])
	}
}

func TestFixtureToolInteractions(t *testing.T) {
	s := parseFixture(t, harness.Options{})

	ti := s.ToolInteractions()
	if len(ti) != 5 {
		t.Fatalf("got %d tool interactions, want 5 (4 calls + 1 orphan)", len(ti))
	}

	// call-1: bash, answered.
	c1 := ti[0].Call.ToolCall
	if c1.ToolCallID != "call-1" || c1.Name != "bash" || c1.Kind != session.ToolKindExecute {
		t.Errorf("call-1 = %+v", c1)
	}
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(c1.Input, &args); err != nil || args.Command != "cat note.txt" {
		t.Errorf("call-1 input = %s (err %v)", c1.Input, err)
	}
	if len(ti[0].Results) != 1 {
		t.Fatalf("call-1 results = %d, want 1", len(ti[0].Results))
	}
	r1 := ti[0].Results[0].ToolResult
	if r1.ToolName != "bash" || r1.IsError || !strings.HasPrefix(r1.Text(), "hello\n") {
		t.Errorf("call-1 result = %+v", r1)
	}
	if len(r1.Enrichment) == 0 || !bytes.Contains(r1.Enrichment, []byte(`"shellExecution"`)) {
		t.Error("call-1 result: want the full data payload in enrichment")
	}

	// call-2: create, denied.
	c2 := ti[1].Call.ToolCall
	if c2.Name != "create" || c2.Kind != session.ToolKindEdit {
		t.Errorf("call-2 = %+v", c2)
	}
	r2 := ti[1].Results[0].ToolResult
	if !r2.IsError || r2.Text() != "Permission denied and could not request permission from user" {
		t.Errorf("call-2 result = %+v, want error with the denial message", r2)
	}

	// call-3 and call-4 are parallel requests in one message; call-4 is
	// never executed (the abort).
	if ti[2].Call.ToolCall.ToolCallID != "call-3" || len(ti[2].Results) != 1 {
		t.Errorf("call-3 = %+v", ti[2])
	}
	if ti[3].Call.ToolCall.ToolCallID != "call-4" || len(ti[3].Results) != 0 {
		t.Errorf("call-4 = %+v, want unanswered", ti[3])
	}
	if ti[2].Call.MessageID != ti[3].Call.MessageID {
		t.Error("parallel calls must share a message id")
	}

	// The orphan result is named from its tool.execution_start.
	orphan := ti[4]
	if orphan.Call != nil || len(orphan.Results) != 1 || orphan.Results[0].ToolResult.ToolName != "view" {
		t.Errorf("orphan = %+v", orphan)
	}

	st := s.Stats()
	if st.ToolCalls != 4 || st.ToolErrors != 1 || st.UnansweredCalls != 1 || st.OrphanResults != 1 {
		t.Errorf("stats = calls %d errors %d unanswered %d orphans %d", st.ToolCalls, st.ToolErrors, st.UnansweredCalls, st.OrphanResults)
	}
}

func TestFixtureSystemEvents(t *testing.T) {
	s := parseFixture(t, harness.Options{})
	st := s.Stats()
	want := map[string]int{
		"session.permissions_changed": 2,
		"session.model_change":        1,
		"system.message":              1,
		"assistant.turn_start":        4,
		"assistant.turn_end":          3,
		"tool.execution_start":        4,
		"permission.requested":        1,
		"permission.completed":        1,
		"session.usage_checkpoint":    1,
		"session.shutdown":            2,
		"session.resume":              1,
		"abort":                       1,
		"session.info":                1,
	}
	for k, n := range want {
		if st.SystemBySubtype[k] != n {
			t.Errorf("SystemBySubtype[%s] = %d, want %d", k, st.SystemBySubtype[k], n)
		}
	}
	if len(st.SystemBySubtype) != len(want) {
		t.Errorf("SystemBySubtype = %v", st.SystemBySubtype)
	}
	for _, ev := range s.Events {
		if ev.Kind != session.KindSystem {
			continue
		}
		if len(ev.System.Details) == 0 {
			t.Errorf("system/%s: no details", ev.System.Subtype)
		}
		switch ev.System.Subtype {
		case "system.message":
			if !strings.HasPrefix(ev.System.Text, "You are a synthetic test assistant.") {
				t.Errorf("system.message text = %q", ev.System.Text)
			}
		case "abort":
			if ev.System.Text != "user_initiated" {
				t.Errorf("abort text = %q", ev.System.Text)
			}
		case "session.info":
			if !strings.Contains(ev.System.Text, "trusted folders") {
				t.Errorf("session.info text = %q", ev.System.Text)
			}
		}
	}
}

func TestFixtureUserOrigins(t *testing.T) {
	s := parseFixture(t, harness.Options{})
	var texts []string
	for _, ev := range s.Events {
		if ev.Kind == session.KindUserMessage {
			if ev.UserMessage.Origin != session.OriginHuman {
				t.Errorf("origin = %q, want human", ev.UserMessage.Origin)
			}
			texts = append(texts, ev.UserMessage.Text())
		}
	}
	if len(texts) != 2 || !strings.HasPrefix(texts[0], "Run cat note.txt") || strings.Contains(texts[0], "current_datetime") {
		t.Errorf("user texts = %q (want the submitted content, not transformedContent)", texts)
	}
}

func TestFixtureLineAccounting(t *testing.T) {
	data := fixtureBytes(t)
	var skips []int
	s, err := harness.Parse(Adapter{}, bytes.NewReader(data), harness.Options{
		OnSkip: func(line int, _ string) { skips = append(skips, line) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(skips) != 0 {
		t.Errorf("skips = %v, want none", skips)
	}
	for _, ev := range s.Events {
		if ev.Provenance == nil {
			t.Errorf("event kind %q has no provenance", ev.Kind)
		}
	}
	if un := parseutil.UncoveredLines(data, s.Events, skips); len(un) > 0 {
		t.Errorf("lines not covered by any event or skip: %v", un)
	}
}

func TestUnknownTelemetryTypeBecomesSystem(t *testing.T) {
	input := startLine("s", "/tmp", "2026-09-25T15:00:00.000Z") +
		`{"type":"session.future_thing","data":{"x":1},"id":"e-2","timestamp":"2026-09-25T15:00:01.000Z","parentId":"e-1"}` + "\n" +
		`{"type":"permission.future","data":{"y":2},"id":"e-3","timestamp":"2026-09-25T15:00:02.000Z","parentId":"e-2"}` + "\n"
	s, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{})
	if err != nil {
		t.Fatalf("unknown telemetry type must not fail: %v", err)
	}
	if len(s.Events) != 3 {
		t.Fatalf("got %d events, want 3", len(s.Events))
	}
	for i, want := range []string{"session.future_thing", "permission.future"} {
		ev := s.Events[i+1]
		if ev.Kind != session.KindSystem || ev.System.Subtype != want || len(ev.System.Details) == 0 {
			t.Errorf("event %d = %+v, want system/%s with details", i+1, ev, want)
		}
	}
}

func TestUnknownRecordTypeFailsLoudly(t *testing.T) {
	input := startLine("s", "/tmp", "2026-09-25T15:00:00.000Z") +
		`{"type":"assistant.hologram","data":{"x":1},"id":"e-2","timestamp":"2026-09-25T15:00:01.000Z","parentId":"e-1"}` + "\n"
	_, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{})
	var pe *harness.ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("want ParseError, got %v", err)
	}
	if pe.Line != 2 || pe.Harness != harness.Copilot || pe.HarnessVersion != "1.0.88" {
		t.Errorf("ParseError = %+v", pe)
	}

	s, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{Permissive: true})
	if err != nil {
		t.Fatalf("permissive: %v", err)
	}
	if s.Report.UnknownEvents != 1 || s.Events[1].Unknown.RecordType != "assistant.hologram" {
		t.Errorf("permissive session = report %+v, event %+v", s.Report, s.Events[1])
	}
}

func TestUnknownReasoningBlockFailsLoudly(t *testing.T) {
	input := startLine("s", "/tmp", "2026-09-25T15:00:00.000Z") +
		`{"type":"assistant.message","data":{"messageId":"am","model":"m","content":"","toolRequests":[],"reasoningBlocks":{"provider":"other","blocks":[{"type":"summary","text":"x"}]}},"id":"e-2","timestamp":"2026-09-25T15:00:01.000Z","parentId":"e-1"}` + "\n"
	_, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{})
	var pe *harness.ParseError
	if !errors.As(err, &pe) || pe.Line != 2 || !strings.Contains(pe.Msg, "reasoning block") {
		t.Errorf("err = %v, want a line-2 ParseError about the reasoning block", err)
	}
}

func TestOpenAIReasoningBlock(t *testing.T) {
	input := startLine("s", "/tmp", "2026-09-25T15:00:00.000Z") +
		`{"type":"assistant.message","data":{"messageId":"am","model":"gpt-x","content":"ok","toolRequests":[],"reasoningOpaque":"RID","reasoningBlocks":{"provider":"openai-responses","blocks":[{"content":[],"encrypted_content":"ENC","id":"RID","summary":[{"type":"summary_text","text":"a summary"}],"type":"reasoning"}]}},"id":"e-2","timestamp":"2026-09-25T15:00:01.000Z","parentId":"e-1"}` + "\n"
	s, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Events) != 3 || s.Events[1].Kind != session.KindThinking {
		t.Fatalf("events = %+v", s.Events)
	}
	if th := s.Events[1].Thinking; th.Text != "a summary" || th.Signature != "ENC" {
		t.Errorf("thinking = %+v", th)
	}
}

func TestFlattenedReasoningFallback(t *testing.T) {
	input := startLine("s", "/tmp", "2026-09-25T15:00:00.000Z") +
		`{"type":"assistant.message","data":{"messageId":"am","model":"m","content":"ok","toolRequests":[],"reasoningText":"thought","reasoningOpaque":"OPAQUE"},"id":"e-2","timestamp":"2026-09-25T15:00:01.000Z","parentId":"e-1"}` + "\n"
	s, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Events) != 3 || s.Events[1].Kind != session.KindThinking {
		t.Fatalf("events = %+v", s.Events)
	}
	if th := s.Events[1].Thinking; th.Text != "thought" || th.Signature != "OPAQUE" {
		t.Errorf("thinking = %+v", th)
	}
}

func TestWebFetchPromotesRetrievalMetrics(t *testing.T) {
	input := startLine("s", "/tmp", "2026-09-25T15:00:00.000Z") +
		`{"type":"assistant.message","data":{"messageId":"am","model":"m","content":"","toolRequests":[{"toolCallId":"c-1","name":"web_fetch","arguments":{"url":"https://example.com/"},"type":"function"}]},"id":"e-2","timestamp":"2026-09-25T15:00:01.000Z","parentId":"e-1"}` + "\n" +
		`{"type":"tool.execution_complete","data":{"toolCallId":"c-1","success":true,"result":{"content":"Contents of https://example.com/:\n# Example Domain"},"toolTelemetry":{"properties":{"hashedUrl":"abc","raw":"false"},"restrictedProperties":{"url":"https://example.com/"},"metrics":{"startIndex":0.0,"httpStatusCode":200,"originalContentLength":167.0,"returnedContentLength":167}}},"id":"e-3","timestamp":"2026-09-25T15:00:02.000Z","parentId":"e-2"}` + "\n"
	s, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ti := s.ToolInteractions()
	if len(ti) != 1 || ti[0].Call.ToolCall.Kind != session.ToolKindFetch || len(ti[0].Results) != 1 {
		t.Fatalf("interactions = %+v", ti)
	}
	f := ti[0].Results[0].ToolResult.Fetch
	if f == nil || f.URL != "https://example.com/" || f.StatusCode != 200 || f.RawBytes != 0 {
		t.Errorf("fetch = %+v, want url and status only", f)
	}
}

func TestMalformedJSONLineNumber(t *testing.T) {
	input := startLine("s", "/tmp", "2026-09-25T15:00:00.000Z") + "\n" + `{"type":` + "\n"
	_, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{})
	var pe *harness.ParseError
	if !errors.As(err, &pe) || pe.Line != 3 {
		t.Errorf("err = %v, want ParseError at line 3", err)
	}
}

func TestSparseMetaWithoutSessionStart(t *testing.T) {
	// A transcript whose head lacks session.start still leads with a
	// (sparse) session_meta, then the record itself.
	input := `{"type":"assistant.turn_start","data":{"turnId":"0"},"id":"e-9","timestamp":"2026-09-25T15:00:01.000Z","parentId":"e-8"}` + "\n"
	s, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{HarnessVersionHint: "1.0.90"})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Events) != 2 || s.Events[0].Kind != session.KindSessionMeta || s.Events[1].Kind != session.KindSystem {
		t.Fatalf("events = %+v", s.Events)
	}
	if s.Meta.SessionID != "" || s.Meta.HarnessVersion != "1.0.90" {
		t.Errorf("meta = %+v, want sparse with the hint", s.Meta)
	}
	if un := parseutil.UncoveredLines([]byte(input), s.Events, nil); len(un) > 0 {
		t.Errorf("uncovered lines: %v", un)
	}
}

func TestHarnessVersionHintLosesToTranscript(t *testing.T) {
	s := parseFixture(t, harness.Options{HarnessVersionHint: "99.0.0"})
	if s.Meta.HarnessVersion != "1.0.88" {
		t.Errorf("HarnessVersion = %q, want the transcript's own 1.0.88", s.Meta.HarnessVersion)
	}
}

func TestPermissiveMalformedStartStillLeadsWithSessionMeta(t *testing.T) {
	input := `{"type":"session.start","data":"not an object","id":"e-1","timestamp":"2026-09-25T15:00:00.000Z","parentId":null}` + "\n"
	s, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{Permissive: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Events) != 2 || s.Events[0].Kind != session.KindSessionMeta || s.Events[1].Kind != session.KindUnknown {
		t.Errorf("events = %+v, want sparse meta + unknown", s.Events)
	}
	if _, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{}); err == nil {
		t.Error("strict parse of malformed session.start: want error")
	}
}

func TestKeepRawAndTruncate(t *testing.T) {
	s := parseFixture(t, harness.Options{KeepRaw: true, MaxPayloadBytes: 4})
	for _, ev := range s.Events {
		if ev.Provenance == nil || len(ev.Provenance.Raw) != 1 {
			t.Errorf("%s at line %d: want one raw record", ev.Kind, ev.Provenance.Line)
		}
		if ev.Kind != session.KindToolResult {
			continue
		}
		for _, b := range ev.ToolResult.Content {
			if !b.Truncated || b.Size <= 4 || b.Digest == "" || b.Text != "" {
				t.Errorf("result at line %d: block %+v not truncated", ev.Provenance.Line, b)
			}
		}
		var placeholder struct {
			Truncated bool `json:"truncated"`
		}
		if json.Unmarshal(ev.ToolResult.Enrichment, &placeholder) != nil || !placeholder.Truncated {
			t.Errorf("result at line %d: enrichment not truncated: %s", ev.Provenance.Line, ev.ToolResult.Enrichment)
		}
	}
}

func TestCRLFAndBOM(t *testing.T) {
	data := bytes.ReplaceAll(fixtureBytes(t), []byte("\n"), []byte("\r\n"))
	data = append([]byte("\xef\xbb\xbf"), data...)
	s, err := harness.Parse(Adapter{}, bytes.NewReader(data), harness.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Meta.SessionID != "sess-copilot-1" || len(s.Events) != 39 {
		t.Errorf("meta %+v, %d events", s.Meta, len(s.Events))
	}
}

func TestSniff(t *testing.T) {
	fixture := fixtureBytes(t)
	a := Adapter{}
	firstLine, _, _ := bytes.Cut(fixture, []byte("\n"))
	if got := a.Sniff(append(firstLine, '\n')); got != harness.Certain {
		t.Errorf("Sniff(fixture head) = %v, want Certain", got)
	}
	// Header cut off inside the first line: marker fallback.
	if got := a.Sniff(fixture[:120]); got != harness.Possible {
		t.Errorf("Sniff(truncated header) = %v, want Possible", got)
	}
	midFile := `{"type":"assistant.turn_start","data":{"turnId":"0"},"id":"e-9","timestamp":"2026-09-25T15:00:01.000Z","parentId":"e-8"}`
	if got := a.Sniff([]byte(midFile)); got != harness.Possible {
		t.Errorf("Sniff(mid-file record) = %v, want Possible", got)
	}
	claudeLine := `{"parentUuid":null,"isSidechain":false,"type":"user","message":{"role":"user","content":"hi"},"uuid":"u-1","sessionId":"s-1"}`
	if got := a.Sniff([]byte(claudeLine)); got != harness.NoMatch {
		t.Errorf("Sniff(claude-code line) = %v, want NoMatch", got)
	}
	codexLine := `{"timestamp":"2026-07-19T22:00:00.000Z","type":"session_meta","payload":{"session_id":"s-1","cli_version":"0.144.1","cwd":"/tmp"}}`
	if got := a.Sniff([]byte(codexLine)); got != harness.NoMatch {
		t.Errorf("Sniff(codex line) = %v, want NoMatch", got)
	}
	agyLine := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-07-19T23:00:00Z","content":"hi"}`
	if got := a.Sniff([]byte(agyLine)); got != harness.NoMatch {
		t.Errorf("Sniff(antigravity line) = %v, want NoMatch", got)
	}
	if got := a.Sniff([]byte("garbage")); got != harness.NoMatch {
		t.Errorf("Sniff(garbage) = %v, want NoMatch", got)
	}
}

// TestLocalTranscripts parses every real events.jsonl under the directory
// in AGENTMINUTES_LOCAL_COPILOT_TRANSCRIPTS (e.g. ~/.copilot/session-state),
// strictly, with per-line accounting.
func TestLocalTranscripts(t *testing.T) {
	dir := os.Getenv("AGENTMINUTES_LOCAL_COPILOT_TRANSCRIPTS")
	if dir == "" {
		t.Skip("set AGENTMINUTES_LOCAL_COPILOT_TRANSCRIPTS to run against real transcripts")
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*", transcriptName))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("no transcripts under %s", dir)
	}
	var events int
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var skips []int
		s, err := harness.Parse(Adapter{}, bytes.NewReader(data), harness.Options{
			OnSkip: func(line int, _ string) { skips = append(skips, line) },
		})
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		events += len(s.Events)
		if s.Meta.SessionID == "" {
			t.Errorf("%s: no session id in meta", path)
		}
		if un := parseutil.UncoveredLines(data, s.Events, skips); len(un) > 0 {
			t.Errorf("%s: %d lines not covered by any event or skip (first: line %d)", path, len(un), un[0])
		}
		textaudit.Invariant(t, path, s.Events, nonText)
		deliveryInvariant(t, path, s.Events, harness.TextBare)
	}
	t.Logf("parsed %d transcripts: %d events", len(paths), events)
}
