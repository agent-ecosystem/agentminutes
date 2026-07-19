package codex

import (
	"strings"
	"testing"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/parseutil"
	"github.com/agent-ecosystem/agentminutes/session"
)

// webSearchEndLine is the shape observed via drift probe on 0.144.1: the
// only trace of a URL fetch on that version.
const webSearchEndLine = `{"timestamp":"2026-07-19T22:00:02.000Z","type":"event_msg","payload":{"type":"web_search_end","call_id":"exec-abc","query":"https://example.com/","action":{"type":"open_page","url":"https://example.com/"}}}`

func TestPromoteWebSearch(t *testing.T) {
	input := `{"timestamp":"2026-07-19T22:00:00.000Z","type":"session_meta","payload":{"session_id":"s","cli_version":"0.144.1","cwd":"/tmp"}}` + "\n" +
		webSearchEndLine + "\n"

	// Without the transform: telemetry stays a system event.
	s, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.ToolInteractions()) != 0 {
		t.Fatalf("without transform: %d tool interactions, want 0", len(s.ToolInteractions()))
	}
	last := s.Events[len(s.Events)-1]
	if last.Kind != session.KindSystem || last.System.Subtype != "web_search_end" {
		t.Fatalf("without transform: last event %+v, want system/web_search_end", last)
	}

	// With the transform: a synthesized, marked fetch pair replaces it.
	var skips []int
	s, err = harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{
		OnSkip: func(line int, _ string) { skips = append(skips, line) },
	}, PromoteWebSearch)
	if err != nil {
		t.Fatal(err)
	}
	ti := s.ToolInteractions()
	if len(ti) != 1 || ti[0].Call == nil || len(ti[0].Results) != 1 {
		t.Fatalf("with transform: interactions = %+v, want one complete pair", ti)
	}
	call, result := ti[0].Call.ToolCall, ti[0].Results[0].ToolResult
	if call.ToolCallID != "exec-abc" || call.Name != "web_search" || call.Kind != session.ToolKindFetch {
		t.Errorf("call = %+v", call)
	}
	if call.PromotedFrom != "event_msg/web_search_end" || result.PromotedFrom != "event_msg/web_search_end" {
		t.Errorf("promoted_from = %q / %q, want the marker on both", call.PromotedFrom, result.PromotedFrom)
	}
	if !strings.Contains(string(call.Input), "open_page") {
		t.Errorf("call input = %s, want the action", call.Input)
	}
	if len(result.Enrichment) == 0 {
		t.Error("want the verbatim payload in enrichment")
	}
	for i := range s.Events {
		if s.Events[i].Kind == session.KindSystem && s.Events[i].System.Subtype == "web_search_end" {
			t.Error("promotion must replace the system event, not duplicate it")
		}
	}

	// Line accounting holds: the synthesized pair carries the source
	// event's provenance.
	if un := parseutil.UncoveredLines([]byte(input), s.Events, skips); un != nil {
		t.Errorf("uncovered lines with transform: %v", un)
	}
}

// TestPromoteWebSearchDoubleCountAuditable pins the safety story: on a
// transcript carrying both the 0.118 response_item and the 0.144
// telemetry, promotion double-counts, and the marker is what lets a
// consumer drop the promoted pair.
func TestPromoteWebSearchDoubleCountAuditable(t *testing.T) {
	input := `{"timestamp":"2026-07-19T22:00:00.000Z","type":"session_meta","payload":{"session_id":"s","cli_version":"0.144.1","cwd":"/tmp"}}` + "\n" +
		`{"timestamp":"2026-07-19T22:00:01.000Z","type":"response_item","payload":{"type":"web_search_call","status":"completed","action":{"type":"open_page","url":"https://example.com/"}}}` + "\n" +
		webSearchEndLine + "\n"

	s, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{}, PromoteWebSearch)
	if err != nil {
		t.Fatal(err)
	}
	ti := s.ToolInteractions()
	if len(ti) != 2 {
		t.Fatalf("interactions = %d, want 2 (the double count)", len(ti))
	}
	var native, promoted int
	for _, i := range ti {
		if i.Call.ToolCall.PromotedFrom == "" {
			native++
		} else {
			promoted++
		}
	}
	if native != 1 || promoted != 1 {
		t.Errorf("native/promoted = %d/%d, want the marker to distinguish exactly one of each", native, promoted)
	}
}

func TestPromoteWebSearchFallbackID(t *testing.T) {
	input := `{"timestamp":"2026-07-19T22:00:00.000Z","type":"session_meta","payload":{"session_id":"s","cli_version":"0.144.1","cwd":"/tmp"}}` + "\n" +
		`{"timestamp":"2026-07-19T22:00:02.000Z","type":"event_msg","payload":{"type":"web_search_end","query":"x"}}` + "\n"
	s, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{}, PromoteWebSearch)
	if err != nil {
		t.Fatal(err)
	}
	ti := s.ToolInteractions()
	if len(ti) != 1 || ti[0].Call.ToolCall.ToolCallID != "web_search_end:L2" {
		t.Fatalf("interactions = %+v, want line-derived ID without call_id", ti)
	}
}

func TestPromoteWebSearchErrorPassthrough(t *testing.T) {
	input := `{"timestamp":"2026-07-19T22:00:00.000Z","type":"session_meta","payload":{"session_id":"s","cli_version":"0.144.1","cwd":"/tmp"}}` + "\n" +
		`not json` + "\n"
	_, err := harness.Parse(Adapter{}, strings.NewReader(input), harness.Options{}, PromoteWebSearch)
	if err == nil {
		t.Fatal("want the parse error to pass through the transform")
	}
}
