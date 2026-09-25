package copilot

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

// The subagents fixture models a 1.0.88 session that delegates twice: a
// task subagent (its own agent id, an OpenAI-served model, one bash call)
// and a search_code_subagent (agent id equal to the parent's tool call id,
// the constrained file_search/grep_search/read_file toolset). Both
// conversations are interleaved in the parent transcript, stamped with
// agentId, and bracketed by subagent.* records.
func parseNamed(t *testing.T, name string, opts harness.Options) (*session.Session, []byte, []int) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var skips []int
	if opts.OnSkip == nil {
		opts.OnSkip = func(line int, _ string) { skips = append(skips, line) }
	}
	s, err := harness.Parse(Adapter{}, bytes.NewReader(data), opts)
	if err != nil {
		t.Fatal(err)
	}
	return s, data, skips
}

func TestSubagentsAccounting(t *testing.T) {
	s, data, skips := parseNamed(t, "subagents.jsonl", harness.Options{})
	if len(skips) != 0 {
		t.Errorf("skips = %v, want none", skips)
	}
	if un := parseutil.UncoveredLines(data, s.Events, skips); len(un) > 0 {
		t.Errorf("lines not covered: %v", un)
	}
	if s.Meta.SessionID != "sess-copilot-sub" || s.Totals != nil || s.Report.OrphanToolResults != 0 {
		t.Errorf("meta %+v totals %+v report %+v", s.Meta, s.Totals, s.Report)
	}
}

func TestSubagentsAttribution(t *testing.T) {
	s, _, _ := parseNamed(t, "subagents.jsonl", harness.Options{})

	byAgent := map[string]map[session.EventKind]int{}
	for _, ev := range s.Events {
		if byAgent[ev.AgentID] == nil {
			byAgent[ev.AgentID] = map[session.EventKind]int{}
		}
		byAgent[ev.AgentID][ev.Kind]++
	}
	if len(byAgent) != 3 {
		t.Fatalf("agents = %v, want parent + 2 subagents", byAgent)
	}
	parent, task, search := byAgent[""], byAgent["agent-a"], byAgent["call-search"]
	if parent[session.KindAssistantMessage] != 3 || parent[session.KindToolCall] != 2 || parent[session.KindUserMessage] != 1 {
		t.Errorf("parent counts = %v", parent)
	}
	if task[session.KindAssistantMessage] != 2 || task[session.KindToolCall] != 1 || task[session.KindThinking] != 1 || task[session.KindUserMessage] != 1 {
		t.Errorf("task subagent counts = %v", task)
	}
	if search[session.KindAssistantMessage] != 3 || search[session.KindToolCall] != 3 || search[session.KindUserMessage] != 1 {
		t.Errorf("search subagent counts = %v", search)
	}

	// A subagent's prompt is authored by the parent agent, not the human.
	var origins []session.Origin
	for _, ev := range s.Events {
		if ev.Kind == session.KindUserMessage {
			origins = append(origins, ev.UserMessage.Origin)
		}
	}
	if len(origins) != 3 || origins[0] != session.OriginHuman || origins[1] != session.OriginHarness || origins[2] != session.OriginHarness {
		t.Errorf("origins = %v, want [human harness harness]", origins)
	}
	st := s.Stats()
	if st.UserMessages != 1 || st.HarnessMessages != 2 {
		t.Errorf("stats user %d harness %d", st.UserMessages, st.HarnessMessages)
	}
	if want := []string{"claude-sonnet-5", "gpt-5.6-luna", "copilot-search-a"}; strings.Join(st.Models, ",") != strings.Join(want, ",") {
		t.Errorf("models = %v, want %v", st.Models, want)
	}
}

func TestSubagentsToolsAndLifecycle(t *testing.T) {
	s, _, _ := parseNamed(t, "subagents.jsonl", harness.Options{})

	kinds := map[string]session.ToolKind{}
	answered := map[string]int{}
	for _, ti := range s.ToolInteractions() {
		if ti.Call == nil {
			t.Errorf("orphan result %+v", ti.Results[0].ToolResult)
			continue
		}
		kinds[ti.Call.ToolCall.Name] = ti.Call.ToolCall.Kind
		answered[ti.Call.ToolCall.ToolCallID] = len(ti.Results)
	}
	want := map[string]session.ToolKind{
		"task":                 session.ToolKindOther,
		"search_code_subagent": session.ToolKindOther,
		"bash":                 session.ToolKindExecute,
		"file_search":          session.ToolKindSearch,
		"grep_search":          session.ToolKindSearch,
		"read_file":            session.ToolKindRead,
	}
	for name, k := range want {
		if kinds[name] != k {
			t.Errorf("kind[%s] = %q, want %q", name, kinds[name], k)
		}
	}
	for id, n := range answered {
		if n != 1 {
			t.Errorf("call %s has %d results, want 1", id, n)
		}
	}
	if len(answered) != 6 {
		t.Errorf("%d calls, want 6", len(answered))
	}

	// The parent's task result is the subagent's final text, and the
	// lifecycle records tie the agent id to the parent's tool call.
	for _, ti := range s.ToolInteractions() {
		if ti.Call.ToolCall.ToolCallID == "call-task" && ti.Results[0].ToolResult.Text() != "from-subagent" {
			t.Errorf("task result = %q", ti.Results[0].ToolResult.Text())
		}
	}
	st := s.Stats()
	for subtype, n := range map[string]int{"subagent.started": 2, "subagent.configured": 2, "subagent.selected": 1, "subagent.completed": 2, "session.model_change": 3} {
		if st.SystemBySubtype[subtype] != n {
			t.Errorf("SystemBySubtype[%s] = %d, want %d", subtype, st.SystemBySubtype[subtype], n)
		}
	}
	for _, ev := range s.Events {
		if ev.Kind == session.KindSystem && ev.System.Subtype == "subagent.started" && ev.AgentID == "" {
			t.Error("subagent.started without agent id")
		}
		if ev.Kind == session.KindThinking && ev.AgentID == "agent-a" {
			if ev.Thinking.Text != "**Running the command**\nI will run it and report the output." && !strings.HasPrefix(ev.Thinking.Text, "**Running the command**") || ev.Thinking.Signature != "ENC-1" {
				t.Errorf("subagent thinking = %+v", ev.Thinking)
			}
		}
	}
}

// The tools fixture models a 1.0.88 session on a Gemini-served model that
// exercises view, edit, glob and grep in parallel (results out of order),
// a background bash with read_bash/list_bash/stop_bash, web_fetch, and a
// built-in GitHub MCP tool.
func TestToolsFixture(t *testing.T) {
	s, data, skips := parseNamed(t, "tools.jsonl", harness.Options{})
	if len(skips) != 0 {
		t.Errorf("skips = %v, want none", skips)
	}
	if un := parseutil.UncoveredLines(data, s.Events, skips); len(un) > 0 {
		t.Errorf("lines not covered: %v", un)
	}
	if s.Meta.SessionID != "sess-copilot-tools" || s.Meta.HarnessVersion != "1.0.88" || s.Totals != nil {
		t.Errorf("meta %+v totals %+v", s.Meta, s.Totals)
	}
	st := s.Stats()
	if st.ToolCalls != 10 || st.ToolErrors != 0 || st.UnansweredCalls != 0 || st.OrphanResults != 0 {
		t.Errorf("stats = calls %d errors %d unanswered %d orphans %d", st.ToolCalls, st.ToolErrors, st.UnansweredCalls, st.OrphanResults)
	}
	wantKinds := map[session.ToolKind]int{
		session.ToolKindRead:    2, // view, read_bash
		session.ToolKindEdit:    1, // edit
		session.ToolKindSearch:  2, // glob, grep
		session.ToolKindExecute: 2, // bash, stop_bash
		session.ToolKindFetch:   1, // web_fetch
		session.ToolKindOther:   2, // list_bash, github-mcp-server-get_file_contents
	}
	for k, n := range wantKinds {
		if st.ToolCallsByKind[k] != n {
			t.Errorf("ToolCallsByKind[%s] = %d, want %d", k, st.ToolCallsByKind[k], n)
		}
	}
	if st.SystemBySubtype["session.model_change"] != 0 || len(st.Models) != 1 || st.Models[0] != "gemini-3.5-flash" {
		t.Errorf("--model session: model_change %d, models %v", st.SystemBySubtype["session.model_change"], st.Models)
	}

	for _, ti := range s.ToolInteractions() {
		call, res := ti.Call.ToolCall, ti.Results[0].ToolResult
		if res.ToolName != call.Name {
			t.Errorf("result for %s named %q", call.Name, res.ToolName)
		}
		switch call.Name {
		case "grep":
			if res.Text() != "./notes/alpha.txt" {
				t.Errorf("grep result = %q (out-of-order pairing)", res.Text())
			}
		case "glob":
			if !strings.HasPrefix(res.Text(), "./notes/beta.txt") {
				t.Errorf("glob result = %q", res.Text())
			}
		case "web_fetch":
			if res.Fetch == nil || res.Fetch.URL != "https://example.com/" || res.Fetch.StatusCode != 200 {
				t.Errorf("fetch = %+v", res.Fetch)
			}
		case "github-mcp-server-get_file_contents":
			if !strings.Contains(res.Text(), "MIT License") || !bytes.Contains(res.Enrichment, []byte("mcp_result_content_bytes")) {
				t.Errorf("mcp result = %+v", res)
			}
		case "bash":
			if !strings.Contains(res.Text(), "started in background") {
				t.Errorf("async bash result = %q", res.Text())
			}
		}
	}

	// Gemini records only the flattened reasoning pair.
	var thinking []*session.Thinking
	for _, ev := range s.Events {
		if ev.Kind == session.KindThinking {
			thinking = append(thinking, ev.Thinking)
		}
	}
	if len(thinking) != 1 || !strings.HasPrefix(thinking[0].Text, "**Planning the file read**") || thinking[0].Signature != "GEM-OPAQUE-1" {
		t.Errorf("thinking = %+v", thinking)
	}
}
