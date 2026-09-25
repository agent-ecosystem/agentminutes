package antigravity

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/parseutil"
	"github.com/agent-ecosystem/agentminutes/session"
)

// The probes fixture pins the 1.2.11 shapes the failure, image, MCP, and
// model probes surfaced on a Claude-served session: a command that exits
// nonzero (no error key, only the templated exit line), three failed
// steps that carry an error key (an absent file, a rejected
// replace_file_content, a 404 fetch), an image view (a media list), a
// call_mcp_tool call, plaintext thinking, and a model-output error the
// harness retried.
func TestProbesFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "probes.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := harness.Parse(Adapter{}, bytes.NewReader(data), harness.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if un := parseutil.UncoveredLines(data, s.Events, nil); len(un) > 0 {
		t.Errorf("lines not covered: %v", un)
	}

	st := s.Stats()
	if st.ToolCalls != 6 || st.ToolErrors != 4 || st.UnansweredCalls != 0 || st.OrphanResults != 0 {
		t.Errorf("stats = calls %d errors %d unanswered %d orphans %d", st.ToolCalls, st.ToolErrors, st.UnansweredCalls, st.OrphanResults)
	}
	for _, ti := range s.ToolInteractions() {
		call, res := ti.Call.ToolCall, ti.Results[0].ToolResult
		switch call.Name {
		case "run_command":
			if !res.IsError {
				t.Error("nonzero exit must be an error despite the missing error key")
			}
		case "view_file":
			if len(res.Content) == 2 {
				if res.Content[1].Kind != session.ContentImage || res.Content[1].MimeType != "image/png" || res.Content[1].URI == "" {
					t.Errorf("image view = %+v", res.Content[1])
				}
				if res.IsError {
					t.Error("image view is not an error")
				}
			} else if !res.IsError {
				t.Errorf("absent-file view = %+v, want error", res)
			}
		case "replace_file_content":
			if call.Kind != session.ToolKindEdit || !res.IsError {
				t.Errorf("replace_file_content = kind %q error %v", call.Kind, res.IsError)
			}
		case "read_url_content":
			if !res.IsError {
				t.Error("404 fetch must be an error")
			}
		case "call_mcp_tool":
			if call.Kind != session.ToolKindOther || res.IsError || res.Text() == "" {
				t.Errorf("mcp = kind %q, result %+v", call.Kind, res)
			}
		}
	}

	var thinking int
	for _, ev := range s.Events {
		if ev.Kind == session.KindThinking && ev.Thinking.Text != "" {
			thinking++
		}
	}
	if thinking != 4 {
		t.Errorf("plaintext thinking events = %d, want 4", thinking)
	}
	if st.SystemBySubtype["error_message"] != 1 {
		t.Errorf("SystemBySubtype = %v, want one error_message", st.SystemBySubtype)
	}
	if st.Models[0] != "Claude Sonnet 4.6 (Thinking)" {
		t.Errorf("models = %v", st.Models)
	}
}

func TestFailedCommand(t *testing.T) {
	if !failedCommand("Created At: x\nCompleted At: y\n\nThe command exited with code 1.\nOutput:\n") {
		t.Error("code 1 not detected")
	}
	if failedCommand("Created At: x\n\nThe command exited with code 0.\nOutput:\nok") {
		t.Error("code 0 flagged")
	}
	if failedCommand("some text mentioning The command exited with code 1. mid-line") {
		t.Error("mid-line mention flagged")
	}
}
