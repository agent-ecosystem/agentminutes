package claudecode

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
	"github.com/agent-ecosystem/agentminutes/session"
)

func parseFixture(t *testing.T, opts harness.Options) *session.Session {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := harness.Parse(Adapter{}, bytes.NewReader(data), opts)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func kinds(events []session.Event) []session.EventKind {
	out := make([]session.EventKind, len(events))
	for i, ev := range events {
		out[i] = ev.Kind
	}
	return out
}

func TestFixtureEventSequence(t *testing.T) {
	s := parseFixture(t, harness.Options{})

	want := []session.EventKind{
		session.KindSessionMeta,      // from line 2
		session.KindUserMessage,      // human question, line 2
		session.KindThinking,         // msg_01, line 3
		session.KindToolCall,         // Read, line 5
		session.KindToolResult,       // Read result, line 7
		session.KindToolCall,         // WebFetch, line 8 (msg_01 resumed)
		session.KindToolResult,       // WebFetch result, line 9
		session.KindAssistantMessage, // msg_01 anchor, closed by msg_02
		session.KindSystem,           // turn_duration, line 11
		session.KindSystem,           // attachment/task_reminder, line 12
		session.KindUserMessage,      // harness-injected reminder, line 13
		session.KindToolResult,       // orphan, line 14
		session.KindAssistantMessage, // msg_02 anchor, closed at EOF
	}
	got := kinds(s.Events)
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
	if m.Harness != "claude-code" || m.HarnessVersion != "2.1.197" ||
		m.SessionID != "s-fixture" || m.CWD != "/tmp/exp" || m.GitBranch != "main" ||
		m.IsSubagent {
		t.Errorf("Meta = %+v", m)
	}

	// msg_01's usage must be the final snapshot (line 8: output 60), not a
	// sum of the four split records; msg_02 adds 300/25.
	if s.Totals == nil || s.Totals.InputTokens != 400 || s.Totals.OutputTokens != 85 {
		t.Errorf("Totals = %+v, want input 400 / output 85", s.Totals)
	}
	if s.Totals == nil || s.Totals.CacheReadInputTokens != 250 || s.Totals.CacheCreationInputTokens != 10 {
		t.Errorf("Totals cache = %+v, want read 250 / creation 10", s.Totals)
	}

	wantSkips := map[string]int{"mode": 1, "file-history-snapshot": 1, "ai-title": 1}
	for k, n := range wantSkips {
		if s.Report.SkippedRecords[k] != n {
			t.Errorf("SkippedRecords[%s] = %d, want %d", k, s.Report.SkippedRecords[k], n)
		}
	}
	if s.Report.OrphanToolResults != 1 {
		t.Errorf("OrphanToolResults = %d, want 1", s.Report.OrphanToolResults)
	}
	if s.Report.UnknownEvents != 0 {
		t.Errorf("UnknownEvents = %d, want 0", s.Report.UnknownEvents)
	}
}

// TestSearchFallbackFixture covers the vocabulary the first consumer's
// smoke tests surfaced (recorded in the inventory): the Glob and Grep
// search tools with their toolUseResult envelopes, and the refusal → model
// fallback → retraction flow (a system record with subtype
// model_refusal_fallback, plus supersedesUuids on the retrying assistant
// record).
func TestSearchFallbackFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "search_fallback.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var skips int
	s, err := harness.Parse(Adapter{}, bytes.NewReader(data), harness.Options{
		OnSkip: func(int, string) { skips++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	if skips != 0 || s.Report.UnknownEvents != 0 || s.Report.OrphanToolResults != 0 {
		t.Errorf("skips %d, unknown %d, orphans %d; want all 0",
			skips, s.Report.UnknownEvents, s.Report.OrphanToolResults)
	}

	search := map[string]bool{}
	for _, ti := range s.ToolInteractions() {
		if ti.Call == nil {
			continue
		}
		call := ti.Call.ToolCall
		if call.Kind != session.ToolKindSearch {
			t.Errorf("tool %q: kind %q, want %q", call.Name, call.Kind, session.ToolKindSearch)
		}
		if len(ti.Results) != 1 {
			t.Errorf("tool %q: %d results, want 1", call.Name, len(ti.Results))
			continue
		}
		if len(ti.Results[0].ToolResult.Enrichment) == 0 {
			t.Errorf("tool %q: no toolUseResult enrichment", call.Name)
		}
		search[call.Name] = true
	}
	if !search["Glob"] || !search["Grep"] {
		t.Errorf("search tools = %v, want Glob and Grep", search)
	}

	subtypes := map[string]int{}
	var fallbackDetails []byte
	for i := range s.Events {
		if ev := &s.Events[i]; ev.Kind == session.KindSystem {
			subtypes[ev.System.Subtype]++
			if ev.System.Subtype == "assistant/fallback" {
				fallbackDetails = ev.System.Details
			}
		}
	}
	if subtypes["model_refusal_fallback"] != 1 || subtypes["assistant/fallback"] != 1 {
		t.Errorf("system subtypes = %v, want one model_refusal_fallback and one assistant/fallback", subtypes)
	}
	var fb struct {
		From, To struct {
			Model string `json:"model"`
		}
	}
	if err := json.Unmarshal(fallbackDetails, &fb); err != nil || fb.From.Model != "claude-fable-5" || fb.To.Model != "claude-opus-4-8" {
		t.Errorf("assistant/fallback details = %s (err %v), want from/to models", fallbackDetails, err)
	}
}

func TestFixtureFoldedMessage(t *testing.T) {
	s := parseFixture(t, harness.Options{})

	var anchors []*session.Event
	for i := range s.Events {
		if s.Events[i].Kind == session.KindAssistantMessage {
			anchors = append(anchors, &s.Events[i])
		}
	}
	if len(anchors) != 2 {
		t.Fatalf("got %d assistant_message events, want 2", len(anchors))
	}

	m1 := anchors[0]
	if m1.MessageID != "msg_01" {
		t.Errorf("anchor 1 MessageID = %q, want msg_01", m1.MessageID)
	}
	am := m1.AssistantMessage
	if am.Text() != "Let me check the documentation." {
		t.Errorf("msg_01 text = %q", am.Text())
	}
	if am.StopReason != "tool_use" || am.Model != "claude-fable-5" {
		t.Errorf("msg_01 stop/model = %q/%q", am.StopReason, am.Model)
	}
	if am.Usage == nil || am.Usage.OutputTokens != 60 {
		t.Errorf("msg_01 usage = %+v, want final snapshot with output 60", am.Usage)
	}
	if am.Usage.Extra == nil {
		t.Error("msg_01 usage Extra: want service_tier preserved")
	}
	// The fold spans lines 3-8 (interleaved records included in the range).
	if m1.Provenance.Line != 3 || m1.Provenance.EndLine != 8 {
		t.Errorf("msg_01 provenance = %d-%d, want 3-8", m1.Provenance.Line, m1.Provenance.EndLine)
	}

	// Thinking and tool calls share msg_01's MessageID.
	for _, ev := range s.Events {
		if ev.Kind == session.KindThinking && ev.MessageID != "msg_01" {
			t.Errorf("thinking MessageID = %q", ev.MessageID)
		}
	}
}

func TestFixtureToolResults(t *testing.T) {
	s := parseFixture(t, harness.Options{})

	ti := s.ToolInteractions()
	if len(ti) != 3 {
		t.Fatalf("got %d tool interactions, want 3 (Read, WebFetch, orphan)", len(ti))
	}

	read := ti[0]
	if read.Call.ToolCall.Name != "Read" || read.Call.ToolCall.Kind != session.ToolKindRead {
		t.Errorf("interaction 0 = %+v, want Read/read", read.Call.ToolCall)
	}
	if len(read.Results) != 1 || read.Results[0].ToolResult.ToolName != "Read" {
		t.Fatalf("Read results = %+v", read.Results)
	}
	if read.Results[0].ToolResult.Text() != "# Doc\nLocks are cool." {
		t.Errorf("Read result text = %q", read.Results[0].ToolResult.Text())
	}
	if len(read.Results[0].ToolResult.Enrichment) == 0 {
		t.Error("Read result: want toolUseResult enrichment")
	}
	if read.Results[0].ToolResult.Fetch != nil {
		t.Error("Read result: want no fetch info")
	}

	fetch := ti[1]
	if fetch.Call.ToolCall.Kind != session.ToolKindFetch {
		t.Errorf("WebFetch kind = %q", fetch.Call.ToolCall.Kind)
	}
	fi := fetch.Results[0].ToolResult.Fetch
	if fi == nil {
		t.Fatal("WebFetch result: want fetch info")
	}
	if fi.URL != "http://localhost:8080/doc" || fi.RawBytes != 52000 ||
		fi.StatusCode != 200 || fi.DurationMS != 812 {
		t.Errorf("FetchInfo = %+v", fi)
	}

	orphan := ti[2]
	if orphan.Call != nil || !orphan.Results[0].ToolResult.IsError {
		t.Errorf("orphan = %+v, want call-less error result", orphan)
	}
}

func TestFixtureOrigins(t *testing.T) {
	s := parseFixture(t, harness.Options{})

	var origins []session.Origin
	for _, ev := range s.Events {
		if ev.Kind == session.KindUserMessage {
			origins = append(origins, ev.UserMessage.Origin)
		}
	}
	if len(origins) != 2 || origins[0] != session.OriginHuman || origins[1] != session.OriginHarness {
		t.Errorf("user message origins = %v, want [human harness]", origins)
	}
}

// TestFixtureLineAccounting enforces the never-silently-drop contract
// mechanically: every source line must be covered by an event's provenance
// range or reported as a skip.
func TestFixtureLineAccounting(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "session.jsonl"))
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
	for _, ev := range s.Events {
		if ev.Provenance == nil {
			t.Errorf("event kind %q has no provenance", ev.Kind)
		}
	}
	if un := parseutil.UncoveredLines(data, s.Events, skips); len(un) > 0 {
		t.Errorf("lines not covered by any event or skip: %v", un)
	}
}

func line(pieces ...string) string { return "{" + strings.Join(pieces, ",") + "}" }

func TestUnrecognizedRecordType(t *testing.T) {
	input := line(`"type":"user"`, `"isSidechain":false`, `"message":{"role":"user","content":"hi"}`,
		`"uuid":"u-1"`, `"timestamp":"2026-07-19T10:00:00.000Z"`, `"userType":"external"`,
		`"entrypoint":"cli"`, `"cwd":"/tmp"`, `"sessionId":"s-x"`, `"version":"2.1.197"`, `"gitBranch":"main"`) +
		"\n" + `{"type":"wat","sessionId":"s-x"}` + "\n"

	_, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{})
	var pe *harness.ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("want ParseError, got %v", err)
	}
	if pe.Line != 2 || pe.Harness != harness.ClaudeCode || pe.HarnessVersion != "2.1.197" {
		t.Errorf("ParseError = %+v", pe)
	}
	if !strings.Contains(pe.Msg, "wat") {
		t.Errorf("Msg = %q, want mention of the record type", pe.Msg)
	}

	s, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{Permissive: true})
	if err != nil {
		t.Fatalf("permissive parse: %v", err)
	}
	if s.Report.UnknownEvents != 1 {
		t.Errorf("UnknownEvents = %d, want 1", s.Report.UnknownEvents)
	}
	last := s.Events[len(s.Events)-1]
	if last.Kind != session.KindUnknown || last.Unknown.RecordType != "wat" || len(last.Unknown.Raw) == 0 {
		t.Errorf("unknown event = %+v", last)
	}
}

func TestMalformedJSON(t *testing.T) {
	_, err := harness.Parse(Adapter{}, strings.NewReader("not json at all\n"), harness.Options{})
	var pe *harness.ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("want ParseError, got %v", err)
	}
	if pe.Line != 1 {
		t.Errorf("Line = %d, want 1", pe.Line)
	}
}

func TestResumedFoldFailsLoudly(t *testing.T) {
	asst := func(uuid, mid, text string) string {
		return line(`"type":"assistant"`, `"isSidechain":false`,
			`"message":{"id":"`+mid+`","model":"m","role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"`+text+`"}],"usage":{"input_tokens":1,"output_tokens":1}}`,
			`"uuid":"`+uuid+`"`, `"timestamp":"2026-07-19T10:00:00.000Z"`, `"userType":"external"`,
			`"entrypoint":"cli"`, `"cwd":"/tmp"`, `"sessionId":"s-x"`, `"version":"2.1.197"`, `"gitBranch":"main"`)
	}
	input := asst("a-1", "msg_A", "one") + "\n" + asst("a-2", "msg_B", "two") + "\n" + asst("a-3", "msg_A", "three") + "\n"

	_, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{})
	var pe *harness.ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("want ParseError, got %v", err)
	}
	if !strings.Contains(pe.Msg, "resumed") {
		t.Errorf("Msg = %q, want fold-resume error", pe.Msg)
	}
}

func TestKeepRaw(t *testing.T) {
	s := parseFixture(t, harness.Options{KeepRaw: true})
	for _, ev := range s.Events {
		if len(ev.Provenance.Raw) == 0 {
			t.Errorf("event kind %q at line %d: no raw records", ev.Kind, ev.Provenance.Line)
		}
	}
	// msg_01's anchor folded four records.
	for _, ev := range s.Events {
		if ev.Kind == session.KindAssistantMessage && ev.MessageID == "msg_01" {
			if len(ev.Provenance.Raw) != 4 {
				t.Errorf("msg_01 anchor raw records = %d, want 4", len(ev.Provenance.Raw))
			}
		}
	}
}

func TestMaxPayloadBytes(t *testing.T) {
	s := parseFixture(t, harness.Options{MaxPayloadBytes: 10})
	for _, ev := range s.Events {
		if ev.Kind != session.KindToolResult {
			continue
		}
		for _, b := range ev.ToolResult.Content {
			if int64(len(b.Text)) > 10 || int64(len(b.Raw)) > 10 {
				t.Errorf("tool result block exceeds limit: %+v", b)
			}
			if b.Truncated && (b.Size == 0 || b.Digest == "") {
				t.Errorf("truncated block missing size/digest: %+v", b)
			}
		}
	}
	// The Read result ("# Doc\nLocks are cool.") must have been truncated.
	found := false
	for _, ti := range s.ToolInteractions() {
		if ti.Call != nil && ti.Call.ToolCall.Name == "Read" {
			found = ti.Results[0].ToolResult.Content[0].Truncated
		}
	}
	if !found {
		t.Error("Read result: want truncated content block")
	}
}

// TestWindowsTranscriptArtifacts guards against the two byte-level
// artifacts a Windows environment introduces: CRLF line endings (a
// transcript written or re-saved on Windows, or a fixture mangled by git
// autocrlf) and a UTF-8 BOM. Both must parse identically to the pristine
// fixture, and both must still sniff as claude-code.
func TestWindowsTranscriptArtifacts(t *testing.T) {
	pristine, err := os.ReadFile(filepath.Join("testdata", "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	mangled := append([]byte("\xef\xbb\xbf"),
		bytes.ReplaceAll(pristine, []byte("\n"), []byte("\r\n"))...)

	want, err := harness.Parse(Adapter{}, bytes.NewReader(pristine), harness.Options{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := harness.Parse(Adapter{}, bytes.NewReader(mangled), harness.Options{})
	if err != nil {
		t.Fatalf("CRLF+BOM transcript: %v", err)
	}

	if len(got.Events) != len(want.Events) {
		t.Fatalf("CRLF+BOM: %d events, pristine %d", len(got.Events), len(want.Events))
	}
	for i := range want.Events {
		if got.Events[i].Kind != want.Events[i].Kind {
			t.Errorf("event %d: kind %q, pristine %q", i, got.Events[i].Kind, want.Events[i].Kind)
		}
	}
	if got.Totals == nil || want.Totals == nil ||
		got.Totals.InputTokens != want.Totals.InputTokens ||
		got.Totals.OutputTokens != want.Totals.OutputTokens {
		t.Errorf("totals differ: %+v vs %+v", got.Totals, want.Totals)
	}

	if conf := (Adapter{}).Sniff(mangled[:512]); conf != harness.Certain {
		t.Errorf("Sniff(CRLF+BOM) = %v, want Certain", conf)
	}
}

func TestHarnessVersionHint(t *testing.T) {
	// The transcript's self-recorded version always wins over the hint.
	s := parseFixture(t, harness.Options{HarnessVersionHint: "99.0.0"})
	if s.Meta.HarnessVersion != "2.1.197" {
		t.Errorf("HarnessVersion = %q, want the transcript's own 2.1.197", s.Meta.HarnessVersion)
	}

	// The hint fills in when records carry no version.
	input := `{"parentUuid":null,"isSidechain":false,"type":"user","message":{"role":"user","content":"hi"},"uuid":"u-1","timestamp":"2026-07-19T10:00:00.000Z","userType":"external","cwd":"/tmp","sessionId":"s-x"}` + "\n"
	s, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{HarnessVersionHint: "2.2.0"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Meta.HarnessVersion != "2.2.0" {
		t.Errorf("HarnessVersion = %q, want the hint", s.Meta.HarnessVersion)
	}
}

func TestSniff(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	a := Adapter{}
	if got := a.Sniff(fixture[:512]); got != harness.Certain {
		t.Errorf("Sniff(fixture) = %v, want Certain", got)
	}
	if got := a.Sniff([]byte("random text, not a transcript")); got != harness.NoMatch {
		t.Errorf("Sniff(garbage) = %v, want NoMatch", got)
	}
	if got := a.Sniff([]byte(`{"unrelated":"json"}`)); got != harness.NoMatch {
		t.Errorf("Sniff(unrelated JSON) = %v, want NoMatch", got)
	}
	if got := a.Sniff([]byte(`{"type":"summary","summary":"old format"}`)); got != harness.Possible {
		t.Errorf("Sniff(summary record) = %v, want Possible", got)
	}
	// Other harnesses' records also carry a "type" field; without a Claude
	// Code envelope marker they must not match, or Detect's alphabetical
	// tie-break would steal them from the right adapter.
	codexMeta := `{"timestamp":"2026-07-19T22:00:00.000Z","type":"session_meta","payload":{"session_id":"s","cli_version":"0.144.1"}}`
	if got := a.Sniff([]byte(codexMeta)); got != harness.NoMatch {
		t.Errorf("Sniff(codex session_meta) = %v, want NoMatch", got)
	}
	codexMidFile := `{"timestamp":"2026-07-19T22:00:01.000Z","type":"response_item","payload":{"type":"message"}}`
	if got := a.Sniff([]byte(codexMidFile)); got != harness.NoMatch {
		t.Errorf("Sniff(codex response_item) = %v, want NoMatch", got)
	}
	agyLine := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-07-19T23:00:00Z","content":"<USER_REQUEST>hi</USER_REQUEST>"}`
	if got := a.Sniff([]byte(agyLine)); got != harness.NoMatch {
		t.Errorf("Sniff(antigravity line) = %v, want NoMatch", got)
	}
}

// TestLocalTranscripts parses every real transcript under the directory in
// AGENTMINUTES_LOCAL_TRANSCRIPTS (e.g. ~/.claude/projects), strictly. It is
// the empirical check that the adapter's record vocabulary is complete.
func TestLocalTranscripts(t *testing.T) {
	dir := os.Getenv("AGENTMINUTES_LOCAL_TRANSCRIPTS")
	if dir == "" {
		t.Skip("set AGENTMINUTES_LOCAL_TRANSCRIPTS to run against real transcripts")
	}
	var paths []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
			paths = append(paths, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("no transcripts under %s", dir)
	}
	var events, skipped int
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
		for _, n := range s.Report.SkippedRecords {
			skipped += n
		}
		if s.Meta.SessionID == "" {
			t.Errorf("%s: no session id in meta", path)
		}
		if un := parseutil.UncoveredLines(data, s.Events, skips); len(un) > 0 {
			t.Errorf("%s: %d lines not covered by any event or skip (first: line %d)", path, len(un), un[0])
		}
	}
	t.Logf("parsed %d transcripts: %d events, %d skipped records", len(paths), events, skipped)
}

// TestEmptyUserContentEmitsEvent pins the accounting invariant for user
// records whose message content is an empty array: the line must still
// produce an event, never vanish.
func TestEmptyUserContentEmitsEvent(t *testing.T) {
	input := line(`"type":"user"`, `"isSidechain":false`, `"message":{"role":"user","content":[]}`,
		`"uuid":"u-1"`, `"timestamp":"2026-07-19T10:00:00.000Z"`, `"userType":"external"`,
		`"entrypoint":"cli"`, `"cwd":"/tmp"`, `"sessionId":"s-1"`, `"version":"2.1.197"`, `"gitBranch":"main"`) + "\n"

	s, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Events) != 2 {
		t.Fatalf("len(Events) = %d, want 2 (meta + empty user message)", len(s.Events))
	}
	ev := s.Events[1]
	if ev.Kind != session.KindUserMessage || len(ev.UserMessage.Content) != 0 {
		t.Errorf("event 1 = %+v, want empty user_message", ev)
	}
	if ev.Provenance == nil || ev.Provenance.Line != 1 {
		t.Errorf("provenance = %+v, want line 1", ev.Provenance)
	}
}
