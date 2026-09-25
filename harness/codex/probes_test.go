package codex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/parseutil"
	"github.com/agent-ecosystem/agentminutes/session"
)

// The probes fixture pins the 0.157.0 shapes the failure, image, and MCP
// probes surfaced: a unified-exec command that exits nonzero (a JSON
// chunk block with exit_code), a failing apply_patch ("Script failed"),
// an exec-wrapped MCP call with its McpToolCall item, an attached image
// in the user message, and a successful command for contrast.
func TestProbesFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "probes.jsonl"))
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

	errors := map[string]bool{}
	for _, ti := range s.ToolInteractions() {
		if ti.Call == nil || len(ti.Results) != 1 {
			t.Errorf("interaction = %+v, want one call with one result", ti)
			continue
		}
		errors[ti.Call.ToolCall.ToolCallID] = ti.Results[0].ToolResult.IsError
	}
	want := map[string]bool{"call-1": true, "call-2": true, "call-3": false, "call-4": false}
	for id, e := range want {
		if errors[id] != e {
			t.Errorf("is_error[%s] = %v, want %v", id, errors[id], e)
		}
	}
	st := s.Stats()
	if st.ToolErrors != 2 || st.ToolCalls != 4 {
		t.Errorf("stats errors %d calls %d", st.ToolErrors, st.ToolCalls)
	}
	if st.SystemBySubtype["item_completed"] != 3 {
		t.Errorf("item_completed = %d, want 3 (user message, command execution, mcp tool call)", st.SystemBySubtype["item_completed"])
	}

	// The attached image is an image block on the user message, verbatim.
	var user *session.UserMessage
	for _, ev := range s.Events {
		if ev.Kind == session.KindUserMessage {
			user = ev.UserMessage
		}
	}
	if user == nil || len(user.Content) != 4 || user.Content[1].Kind != session.ContentImage || !bytes.Contains(user.Content[1].Raw, []byte(`"image_url"`)) {
		t.Errorf("user content = %+v", user)
	}
	if !strings.Contains(user.Text(), "Describe the image") {
		t.Errorf("user text = %q", user.Text())
	}
}

func TestIsErrorOutputUnifiedExec(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want bool
	}{
		{"nonzero chunk", `[{"type":"input_text","text":"Script completed\nWall time 0.1 seconds\nOutput:\n"},{"type":"input_text","text":"{\"exit_code\":1,\"output\":\"x\"}"}]`, true},
		{"zero chunk", `[{"type":"input_text","text":"Script completed\n"},{"type":"input_text","text":"{\"exit_code\":0,\"output\":\"x\"}"}]`, false},
		{"script failed", `[{"type":"input_text","text":"Script failed\nWall time 0.0 seconds\nOutput:\n"},{"type":"input_text","text":"Script error:\nboom"}]`, true},
		{"no chunk", `[{"type":"input_text","text":"Script completed\n"},{"type":"input_text","text":"from-subagent\n"}]`, false},
		{"legacy string", `"{\"metadata\":{\"exit_code\":2}}"`, true},
	}
	for _, c := range cases {
		item := &responseItem{Output: []byte(c.out)}
		if got := isErrorOutput(item); got != c.want {
			t.Errorf("%s: isErrorOutput = %v, want %v", c.name, got, c.want)
		}
	}
}
