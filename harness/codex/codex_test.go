package codex

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
	data, err := os.ReadFile(filepath.Join("testdata", "rollout.jsonl"))
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
		session.KindSessionMeta,      // L1
		session.KindSystem,           // task_started, L2
		session.KindSystem,           // message/developer, L3
		session.KindUserMessage,      // environment_context (harness), L4
		session.KindSystem,           // world_state, L5
		session.KindSystem,           // turn_context, L6
		session.KindUserMessage,      // human prompt, L7
		session.KindThinking,         // L9
		session.KindToolCall,         // exec, L10
		session.KindSystem,           // patch_apply_end, L11
		session.KindToolResult,       // L12
		session.KindSystem,           // token_count, L13
		session.KindSystem,           // token_count, L16
		session.KindAssistantMessage, // anchor (L15), closed by task_complete
		session.KindSystem,           // task_complete, L17
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
	if m.Harness != "codex" || m.HarnessVersion != "0.144.1" ||
		m.SessionID != "sess-codex-1" || m.CWD != "/tmp/exp" || m.GitBranch != "main" {
		t.Errorf("Meta = %+v", m)
	}

	// Pooled per-request usage: 10000/80 + 11000/150.
	if s.Totals == nil || s.Totals.InputTokens != 21000 || s.Totals.OutputTokens != 230 ||
		s.Totals.CacheReadInputTokens != 5000 {
		t.Errorf("Totals = %+v, want 21000/230/5000", s.Totals)
	}

	wantSkips := map[string]int{"event_msg/user_message": 1, "event_msg/agent_message": 1}
	for k, n := range wantSkips {
		if s.Report.SkippedRecords[k] != n {
			t.Errorf("SkippedRecords[%s] = %d, want %d", k, s.Report.SkippedRecords[k], n)
		}
	}
	if s.Report.OrphanToolResults != 0 {
		t.Errorf("OrphanToolResults = %d, want 0", s.Report.OrphanToolResults)
	}
}

func TestFixtureAssistantAnchor(t *testing.T) {
	s := parseFixture(t, harness.Options{})

	var anchor *session.Event
	for i := range s.Events {
		if s.Events[i].Kind == session.KindAssistantMessage {
			anchor = &s.Events[i]
		}
	}
	if anchor == nil {
		t.Fatal("no assistant_message event")
	}
	am := anchor.AssistantMessage
	if am.Model != "gpt-5.6-sol" {
		t.Errorf("model = %q, want gpt-5.6-sol (from turn_context)", am.Model)
	}
	if am.Text() != "Done: go1.26.2, and hello.txt is created." {
		t.Errorf("text = %q", am.Text())
	}
	if am.Usage == nil || am.Usage.InputTokens != 21000 || am.Usage.OutputTokens != 230 {
		t.Errorf("usage = %+v, want pooled 21000/230", am.Usage)
	}
	if anchor.Provenance.Line != 15 {
		t.Errorf("anchor provenance line = %d, want 15", anchor.Provenance.Line)
	}
}

func TestFixtureToolCall(t *testing.T) {
	s := parseFixture(t, harness.Options{})

	ti := s.ToolInteractions()
	if len(ti) != 1 {
		t.Fatalf("got %d tool interactions, want 1", len(ti))
	}
	call := ti[0].Call.ToolCall
	if call.Name != "exec" || call.Kind != session.ToolKindExecute || call.ToolCallID != "call-1" {
		t.Errorf("call = %+v", call)
	}
	// The exec input is a script string, not JSON: it must round-trip as a
	// JSON string.
	var script string
	if err := json.Unmarshal(call.Input, &script); err != nil || !strings.Contains(script, "exec_command") {
		t.Errorf("input = %s (err %v), want JSON-encoded script string", call.Input, err)
	}

	if len(ti[0].Results) != 1 {
		t.Fatalf("results = %d, want 1", len(ti[0].Results))
	}
	tr := ti[0].Results[0].ToolResult
	if tr.ToolName != "exec" || tr.IsError {
		t.Errorf("result = %+v", tr)
	}
	if !strings.Contains(tr.Text(), "go version go1.26.2") {
		t.Errorf("result text = %q", tr.Text())
	}
	if len(tr.Enrichment) == 0 {
		t.Error("result: want full payload in enrichment")
	}
}

func TestFixtureThinkingAndOrigins(t *testing.T) {
	s := parseFixture(t, harness.Options{})

	var thinking *session.Thinking
	var origins []session.Origin
	for _, ev := range s.Events {
		switch ev.Kind {
		case session.KindThinking:
			thinking = ev.Thinking
		case session.KindUserMessage:
			origins = append(origins, ev.UserMessage.Origin)
		}
	}
	if thinking == nil || thinking.Text != "Plan the shell call" || thinking.Signature != "ENCRYPTED1" {
		t.Errorf("thinking = %+v", thinking)
	}
	if len(origins) != 2 || origins[0] != session.OriginHarness || origins[1] != session.OriginHuman {
		t.Errorf("origins = %v, want [harness human]", origins)
	}
}

func TestFixtureLineAccounting(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "rollout.jsonl"))
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

func TestUnknownEventMsgBecomesSystem(t *testing.T) {
	input := `{"timestamp":"2026-07-19T22:00:00.000Z","type":"session_meta","payload":{"session_id":"s","cli_version":"0.144.1","cwd":"/tmp"}}` + "\n" +
		`{"timestamp":"2026-07-19T22:00:01.000Z","type":"event_msg","payload":{"type":"future_telemetry_thing","data":42}}` + "\n"
	s, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{})
	if err != nil {
		t.Fatalf("unknown event_msg must not fail: %v", err)
	}
	last := s.Events[len(s.Events)-1]
	if last.Kind != session.KindSystem || last.System.Subtype != "future_telemetry_thing" {
		t.Errorf("event = %+v, want system/future_telemetry_thing", last)
	}
	if len(last.System.Details) == 0 {
		t.Error("want payload preserved in Details")
	}
}

func TestUnknownResponseItemFailsLoudly(t *testing.T) {
	input := `{"timestamp":"2026-07-19T22:00:00.000Z","type":"session_meta","payload":{"session_id":"s","cli_version":"0.144.1","cwd":"/tmp"}}` + "\n" +
		`{"timestamp":"2026-07-19T22:00:01.000Z","type":"response_item","payload":{"type":"hologram","data":1}}` + "\n"

	_, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{})
	var pe *harness.ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("want ParseError, got %v", err)
	}
	if pe.Line != 2 || pe.Harness != harness.Codex || pe.HarnessVersion != "0.144.1" {
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
	// The transcript's self-recorded cli_version always wins over the hint.
	s := parseFixture(t, harness.Options{HarnessVersionHint: "99.0.0"})
	if s.Meta.HarnessVersion != "0.144.1" {
		t.Errorf("HarnessVersion = %q, want the transcript's own 0.144.1", s.Meta.HarnessVersion)
	}

	// The hint fills in when session_meta records no version.
	input := `{"timestamp":"2026-07-19T22:00:00.000Z","type":"session_meta","payload":{"session_id":"s","cwd":"/tmp"}}` + "\n"
	s, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{HarnessVersionHint: "0.150.0"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Meta.HarnessVersion != "0.150.0" {
		t.Errorf("HarnessVersion = %q, want the hint", s.Meta.HarnessVersion)
	}
}

func TestWebSearchCallPair(t *testing.T) {
	input := `{"timestamp":"2026-07-19T22:00:00.000Z","type":"session_meta","payload":{"session_id":"s","cli_version":"0.144.1","cwd":"/tmp"}}` + "\n" +
		`{"timestamp":"2026-07-19T22:00:01.000Z","type":"response_item","payload":{"type":"web_search_call","status":"completed","action":{"type":"open_page","url":"https://example.com/doc"}}}` + "\n"
	s, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ti := s.ToolInteractions()
	if len(ti) != 1 || ti[0].Call == nil || len(ti[0].Results) != 1 {
		t.Fatalf("interactions = %+v, want one synthesized pair", ti)
	}
	if ti[0].Call.ToolCall.Kind != session.ToolKindFetch {
		t.Errorf("kind = %q, want fetch", ti[0].Call.ToolCall.Kind)
	}
	if len(ti[0].Results[0].ToolResult.Content) != 0 {
		t.Error("web_search result content must be empty (never recorded)")
	}
}

func TestSniff(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "rollout.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	a := Adapter{}
	if got := a.Sniff(fixture[:512]); got != harness.Certain {
		t.Errorf("Sniff(fixture) = %v, want Certain", got)
	}
	midFile := `{"timestamp":"2026-07-19T22:00:01.000Z","type":"response_item","payload":{"type":"message"}}`
	if got := a.Sniff([]byte(midFile)); got != harness.Possible {
		t.Errorf("Sniff(response_item) = %v, want Possible", got)
	}
	claudeLine := `{"parentUuid":null,"isSidechain":false,"type":"user","message":{"role":"user","content":"hi"},"uuid":"u-1","sessionId":"s-1"}`
	if got := a.Sniff([]byte(claudeLine)); got != harness.NoMatch {
		t.Errorf("Sniff(claude-code line) = %v, want NoMatch", got)
	}
	if got := a.Sniff([]byte("garbage")); got != harness.NoMatch {
		t.Errorf("Sniff(garbage) = %v, want NoMatch", got)
	}
}

// TestLocalTranscripts parses every real rollout under the directory in
// AGENTMINUTES_LOCAL_CODEX_TRANSCRIPTS (e.g. ~/.codex/sessions), strictly,
// with per-line accounting.
func TestLocalTranscripts(t *testing.T) {
	dir := os.Getenv("AGENTMINUTES_LOCAL_CODEX_TRANSCRIPTS")
	if dir == "" {
		t.Skip("set AGENTMINUTES_LOCAL_CODEX_TRANSCRIPTS to run against real transcripts")
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

// TestPermissiveMalformedMetaStillLeadsWithSessionMeta pins the stream
// contract for a rollout whose first record is a malformed session_meta: in
// permissive mode the stream still starts with a (sparse) session_meta
// event, followed by the unknown event accounting for the record.
func TestPermissiveMalformedMetaStillLeadsWithSessionMeta(t *testing.T) {
	input := `{"timestamp":"2026-07-19T22:00:00.000Z","type":"session_meta","payload":"not an object"}` + "\n"
	s, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{Permissive: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Events) != 2 {
		t.Fatalf("len(Events) = %d, want 2 (sparse meta + unknown)", len(s.Events))
	}
	if s.Events[0].Kind != session.KindSessionMeta {
		t.Errorf("first event = %q, want session_meta", s.Events[0].Kind)
	}
	if s.Events[1].Kind != session.KindUnknown {
		t.Errorf("second event = %q, want unknown", s.Events[1].Kind)
	}

	// Strict mode still fails loudly.
	if _, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{}); err == nil {
		t.Error("strict parse of malformed session_meta: want error")
	}
}
