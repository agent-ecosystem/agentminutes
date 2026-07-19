package antigravity

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/parseutil"
	"github.com/agent-ecosystem/agentminutes/session"
)

func parseFixture(t *testing.T, opts harness.Options) *session.Session {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "transcript_full.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := harness.Parse(Adapter{}, bytes.NewReader(data), opts)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFixtureEventSequence(t *testing.T) {
	s := parseFixture(t, harness.Options{})

	want := []session.EventKind{
		session.KindSessionMeta,      // step 0, line 1
		session.KindUserMessage,      // step 0: USER_REQUEST body
		session.KindSystem,           // step 0: user_input_context (wrapper)
		session.KindSystem,           // step 1: conversation_history
		session.KindThinking,         // step 2
		session.KindAssistantMessage, // step 2 anchor
		session.KindToolCall,         // step 2: run_command
		session.KindToolResult,       // step 3: RUN_COMMAND
		session.KindSystem,           // step 4: checkpoint (line 10, out of order)
		session.KindAssistantMessage, // step 5 anchor (no text)
		session.KindToolCall,         // step 5: write_to_file
		session.KindToolResult,       // step 6: CODE_ACTION
		session.KindAssistantMessage, // step 7 anchor (no text)
		session.KindToolCall,         // step 7: read_url_content
		session.KindToolResult,       // step 8: READ_URL_CONTENT
		session.KindAssistantMessage, // step 9: final answer
		session.KindSystem,           // step 10: system_message (server notice)
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

func TestFixtureStepOrderSorting(t *testing.T) {
	s := parseFixture(t, harness.Options{})

	// The CHECKPOINT is step 4 but the last line (10) of the file: it must
	// appear at its step position with its true file line in provenance.
	cp := s.Events[8]
	if cp.Kind != session.KindSystem || cp.System.Subtype != "checkpoint" {
		t.Fatalf("event 8 = %+v, want checkpoint system event", cp)
	}
	if cp.Provenance.Line != 10 {
		t.Errorf("checkpoint provenance line = %d, want 10", cp.Provenance.Line)
	}
}

func TestFixtureUserInputSplit(t *testing.T) {
	s := parseFixture(t, harness.Options{})

	um := s.Events[1].UserMessage
	if um.Origin != session.OriginHuman {
		t.Errorf("origin = %q, want human", um.Origin)
	}
	if um.Text() != "Run go version, create hello.txt, and fetch the doc page." {
		t.Errorf("request = %q", um.Text())
	}
	ctx := s.Events[2].System
	if ctx.Subtype != "user_input_context" {
		t.Fatalf("event 2 subtype = %q", ctx.Subtype)
	}
	if !strings.Contains(ctx.Text, "ADDITIONAL_METADATA") || strings.Contains(ctx.Text, "USER_REQUEST>") {
		t.Errorf("context text should hold the wrapper without the request: %q", ctx.Text[:80])
	}
}

func TestFixtureModelAndAnchors(t *testing.T) {
	s := parseFixture(t, harness.Options{})

	var anchors []*session.AssistantMessage
	for _, ev := range s.Events {
		if ev.Kind == session.KindAssistantMessage {
			anchors = append(anchors, ev.AssistantMessage)
		}
	}
	if len(anchors) != 4 {
		t.Fatalf("got %d anchors, want 4", len(anchors))
	}
	for i, am := range anchors {
		if am.Model != "Gemini 3.5 Flash (Medium)" {
			t.Errorf("anchor %d model = %q", i, am.Model)
		}
		if am.Usage != nil {
			t.Errorf("anchor %d: transcripts carry no usage, got %+v", i, am.Usage)
		}
	}
	// No usage anywhere must yield absent totals, not a measured zero.
	if s.Totals != nil {
		t.Errorf("Totals = %+v, want nil for a usage-less format", s.Totals)
	}
	if anchors[0].Text() != "I'll run the command first." {
		t.Errorf("anchor 0 text = %q", anchors[0].Text())
	}
	if anchors[3].Text() != "Done: go1.26.2, hello.txt created, and the doc page fetched." {
		t.Errorf("final anchor text = %q", anchors[3].Text())
	}
}

func TestFixtureToolPairing(t *testing.T) {
	s := parseFixture(t, harness.Options{})

	ti := s.ToolInteractions()
	if len(ti) != 3 {
		t.Fatalf("got %d interactions, want 3", len(ti))
	}
	wantCalls := []struct {
		name string
		kind session.ToolKind
		id   string
	}{
		{"run_command", session.ToolKindExecute, "step-2:call-0"},
		{"write_to_file", session.ToolKindEdit, "step-5:call-0"},
		{"read_url_content", session.ToolKindFetch, "step-7:call-0"},
	}
	for i, w := range wantCalls {
		call := ti[i].Call.ToolCall
		if call.Name != w.name || call.Kind != w.kind || call.ToolCallID != w.id {
			t.Errorf("interaction %d call = %+v, want %+v", i, call, w)
		}
		if len(ti[i].Results) != 1 {
			t.Fatalf("interaction %d: %d results, want 1", i, len(ti[i].Results))
		}
		if ti[i].Results[0].ToolResult.ToolName != w.name {
			t.Errorf("interaction %d result tool = %q", i, ti[i].Results[0].ToolResult.ToolName)
		}
	}
	if s.Report.OrphanToolResults != 0 {
		t.Errorf("orphans = %d, want 0", s.Report.OrphanToolResults)
	}
	if !strings.Contains(ti[0].Results[0].ToolResult.Text(), "go version go1.26.2") {
		t.Errorf("run_command result text = %q", ti[0].Results[0].ToolResult.Text())
	}
	// Full write payload rides in the call input.
	if !strings.Contains(string(ti[1].Call.ToolCall.Input), "hello from antigravity") {
		t.Errorf("write_to_file input = %s", ti[1].Call.ToolCall.Input)
	}
}

func TestFixtureLineAccounting(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "transcript_full.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var skips []int
	s, err := harness.Parse(Adapter{}, bytes.NewReader(data), harness.Options{
		OnSkip: func(line int, _ string) { skips = append(skips, line) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Report.SkippedRecords) != 0 {
		t.Errorf("antigravity has no skip list, got %v", s.Report.SkippedRecords)
	}
	for _, ev := range s.Events {
		if ev.Provenance == nil {
			t.Errorf("event kind %q has no provenance", ev.Kind)
		}
	}
	if un := parseutil.UncoveredLines(data, s.Events, skips); len(un) > 0 {
		t.Errorf("lines not covered by any event: %v", un)
	}
}

func TestOrphanAndAmbiguousRecords(t *testing.T) {
	input := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-07-19T23:00:00Z","content":"<USER_REQUEST>\nhi\n</USER_REQUEST>"}` + "\n" +
		`{"step_index":1,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-07-19T23:00:01Z","content":"Your current permission grants are: ..."}` + "\n" +
		`{"step_index":2,"source":"MODEL","type":"RUN_COMMAND","status":"DONE","created_at":"2026-07-19T23:00:02Z","content":"orphaned output"}` + "\n"
	s, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{})
	if err != nil {
		t.Fatal(err)
	}
	// GENERIC with no pending call: system event, not a tool result.
	var sawGeneric bool
	for _, ev := range s.Events {
		if ev.Kind == session.KindSystem && ev.System.Subtype == "generic" {
			sawGeneric = true
		}
	}
	if !sawGeneric {
		t.Error("GENERIC without pending call: want system event")
	}
	// RUN_COMMAND with no pending call: orphan tool result, preserved.
	if s.Report.OrphanToolResults != 1 {
		t.Errorf("orphans = %d, want 1", s.Report.OrphanToolResults)
	}
}

func TestErrorMessageRecord(t *testing.T) {
	// error_code is numeric in real transcripts; the parse must tolerate it
	// and surface the error as a level-error system event.
	input := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-07-19T23:00:00Z","content":"<USER_REQUEST>\nhi\n</USER_REQUEST>"}` + "\n" +
		`{"step_index":1,"source":"SYSTEM","type":"ERROR_MESSAGE","status":"DONE","error":"There was a problem parsing the tool call.","error_code":13,"created_at":"2026-07-19T23:00:01Z","content":"guidance text"}` + "\n"
	s, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{})
	if err != nil {
		t.Fatal(err)
	}
	last := s.Events[len(s.Events)-1]
	if last.Kind != session.KindSystem || last.System.Level != "error" ||
		!strings.Contains(last.System.Text, "problem parsing") {
		t.Errorf("error event = %+v", last)
	}
}

func TestUnknownRecordType(t *testing.T) {
	input := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-07-19T23:00:00Z","content":"<USER_REQUEST>\nhi\n</USER_REQUEST>"}` + "\n" +
		`{"step_index":1,"source":"MODEL","type":"HOLOGRAM_ACTION","status":"DONE","created_at":"2026-07-19T23:00:01Z","content":"beep"}` + "\n"

	_, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{})
	var pe *harness.ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("want ParseError, got %v", err)
	}
	if pe.Line != 2 || pe.Harness != harness.Antigravity {
		t.Errorf("ParseError = %+v", pe)
	}

	s, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{Permissive: true})
	if err != nil {
		t.Fatalf("permissive: %v", err)
	}
	if s.Report.UnknownEvents != 1 {
		t.Errorf("UnknownEvents = %d, want 1", s.Report.UnknownEvents)
	}
}

func TestHarnessVersionHint(t *testing.T) {
	// The transcript records no version anywhere, so the hint is the only
	// source: it lands in the meta and in parse errors.
	if s := parseFixture(t, harness.Options{}); s.Meta.HarnessVersion != "" {
		t.Errorf("HarnessVersion = %q, want empty without a hint", s.Meta.HarnessVersion)
	}
	s := parseFixture(t, harness.Options{HarnessVersionHint: "1.1.1"})
	if s.Meta.HarnessVersion != "1.1.1" {
		t.Errorf("HarnessVersion = %q, want the hint", s.Meta.HarnessVersion)
	}

	input := `{"step_index":0,"source":"MODEL","type":"HOLOGRAM_ACTION","status":"DONE","created_at":"2026-07-19T23:00:00Z","content":"beep"}` + "\n"
	_, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{HarnessVersionHint: "1.2.3"})
	var pe *harness.ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("want ParseError, got %v", err)
	}
	if pe.HarnessVersion != "1.2.3" {
		t.Errorf("ParseError.HarnessVersion = %q, want the hint", pe.HarnessVersion)
	}
}

func TestSniff(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "transcript_full.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	a := Adapter{}
	if got := a.Sniff(fixture); got != harness.Certain {
		t.Errorf("Sniff(fixture) = %v, want Certain", got)
	}
	// A header cut off mid-first-line still identifies as a possible match.
	if got := a.Sniff(fixture[:200]); got != harness.Possible {
		t.Errorf("Sniff(truncated header) = %v, want Possible", got)
	}
	codexLine := `{"timestamp":"2026-07-19T22:00:00.000Z","type":"session_meta","payload":{"session_id":"s","cli_version":"0.144.1"}}`
	if got := a.Sniff([]byte(codexLine)); got != harness.NoMatch {
		t.Errorf("Sniff(codex line) = %v, want NoMatch", got)
	}
	claudeLine := `{"parentUuid":null,"isSidechain":false,"type":"user","message":{"role":"user","content":"hi"},"uuid":"u-1","sessionId":"s-1"}`
	if got := a.Sniff([]byte(claudeLine)); got != harness.NoMatch {
		t.Errorf("Sniff(claude line) = %v, want NoMatch", got)
	}
	if got := a.Sniff([]byte("garbage")); got != harness.NoMatch {
		t.Errorf("Sniff(garbage) = %v, want NoMatch", got)
	}
}

// TestLocalTranscripts parses every real transcript_full.jsonl under the
// brain directory in AGENTMINUTES_LOCAL_ANTIGRAVITY_TRANSCRIPTS (e.g.
// ~/.gemini/antigravity-cli/brain), strictly, with per-line accounting.
func TestLocalTranscripts(t *testing.T) {
	dir := os.Getenv("AGENTMINUTES_LOCAL_ANTIGRAVITY_TRANSCRIPTS")
	if dir == "" {
		t.Skip("set AGENTMINUTES_LOCAL_ANTIGRAVITY_TRANSCRIPTS to run against real transcripts")
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*", ".system_generated", "logs", "transcript_full.jsonl"))
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
		// Antigravity has no skip list, so provenance alone must cover
		// every line.
		s, err := harness.Parse(Adapter{}, bytes.NewReader(data), harness.Options{})
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		events += len(s.Events)
		if un := parseutil.UncoveredLines(data, s.Events, nil); len(un) > 0 {
			t.Errorf("%s: %d lines not covered by any event (first: line %d)", path, len(un), un[0])
		}
	}
	t.Logf("parsed %d transcripts: %d events", len(paths), events)
}
