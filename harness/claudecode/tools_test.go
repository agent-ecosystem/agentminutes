package claudecode

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/parseutil"
	"github.com/agent-ecosystem/agentminutes/session"
)

// The tools fixture pins 2.1.274 shapes the probe round and the local
// corpus surfaced: a WebFetch that 404s (not is_error; the status is in
// the sidecar), a Read of an image (an image block inside the result), a
// NotebookEdit with its sidecar, an MCP tool call with the attribution
// keys, an unknown-skill error, a custom --agents subagent result with
// the usage sidecar, a SendMessage to a peer session, and the peer's
// hand-back arriving as an isMeta user record with a peer origin.
func TestToolsFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "tools.jsonl"))
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
	if un := parseutil.UncoveredLines(data, s.Events, skips); len(un) > 0 {
		t.Errorf("lines not covered: %v", un)
	}
	st := s.Stats()
	if st.ToolCalls != 7 || st.ToolErrors != 1 || st.UnansweredCalls != 0 || st.OrphanResults != 0 {
		t.Errorf("stats = calls %d errors %d unanswered %d orphans %d", st.ToolCalls, st.ToolErrors, st.UnansweredCalls, st.OrphanResults)
	}
	if st.UserMessages != 1 || st.HarnessMessages != 1 {
		t.Errorf("user %d harness %d, want the prompt and the peer hand-back", st.UserMessages, st.HarnessMessages)
	}
	for _, ti := range s.ToolInteractions() {
		call, res := ti.Call.ToolCall, ti.Results[0].ToolResult
		switch call.Name {
		case "WebFetch":
			if res.IsError || res.Fetch == nil || res.Fetch.StatusCode != 404 || res.Fetch.URL != "https://example.com/nope-404" {
				t.Errorf("WebFetch 404 = error %v fetch %+v", res.IsError, res.Fetch)
			}
		case "Read":
			if len(res.Content) != 1 || res.Content[0].Kind != session.ContentImage || !bytes.Contains(res.Content[0].Raw, []byte(`"media_type":"image/png"`)) {
				t.Errorf("image read = %+v", res.Content)
			}
		case "NotebookEdit":
			if call.Kind != session.ToolKindEdit || !bytes.Contains(res.Enrichment, []byte(`"cell_id"`)) {
				t.Errorf("NotebookEdit = kind %q enrichment %s", call.Kind, res.Enrichment)
			}
		case "mcp__everything__echo":
			if call.Kind != session.ToolKindOther || res.Text() != "Echo: hello-mcp" {
				t.Errorf("mcp = kind %q text %q", call.Kind, res.Text())
			}
		case "Skill":
			if !res.IsError {
				t.Error("unknown skill must be an error")
			}
		case "Agent":
			if !bytes.Contains(res.Enrichment, []byte(`"agentType":"echoer"`)) || !bytes.Contains(res.Enrichment, []byte(`"totalTokens"`)) {
				t.Errorf("custom agent enrichment = %s", res.Enrichment)
			}
		case "SendMessage":
			if call.Kind != session.ToolKindOther || !bytes.Contains(res.Enrichment, []byte(`"pin"`)) {
				t.Errorf("SendMessage = kind %q enrichment %s", call.Kind, res.Enrichment)
			}
		}
	}
}
