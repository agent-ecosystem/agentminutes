package copilot

import (
	"bytes"
	"strings"
	"testing"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/parseutil"
	"github.com/agent-ecosystem/agentminutes/session"
)

// accounted parses a fixture strictly and asserts the per-line accounting
// invariant (no skip list, so provenance alone must cover every line).
func accounted(t *testing.T, name string) *session.Session {
	t.Helper()
	s, data, skips := parseNamed(t, name, harness.Options{})
	if len(skips) != 0 {
		t.Errorf("%s: skips = %v, want none", name, skips)
	}
	if un := parseutil.UncoveredLines(data, s.Events, skips); len(un) > 0 {
		t.Errorf("%s: lines not covered: %v", name, un)
	}
	if s.Totals != nil {
		t.Errorf("%s: totals = %+v, want nil", name, s.Totals)
	}
	return s
}

// The errors fixture models every observed tool failure shape plus an
// image viewed through view, which arrives as a session.binary_asset
// record referenced from the result's binaryResultsForLlm.
func TestErrorsFixture(t *testing.T) {
	s := accounted(t, "errors.jsonl")
	st := s.Stats()
	if st.ToolCalls != 5 || st.ToolErrors != 4 || st.UnansweredCalls != 0 || st.OrphanResults != 0 {
		t.Errorf("stats = calls %d errors %d unanswered %d orphans %d", st.ToolCalls, st.ToolErrors, st.UnansweredCalls, st.OrphanResults)
	}
	for _, ti := range s.ToolInteractions() {
		call, res := ti.Call.ToolCall, ti.Results[0].ToolResult
		switch call.ToolCallID {
		case "call-cat":
			// success: true with a nonzero exit is still a failed call.
			if !res.IsError || !strings.HasPrefix(res.Text(), "cat: missing.txt") {
				t.Errorf("nonzero-exit bash = %+v", res)
			}
		case "call-view":
			if !res.IsError || res.Text() != "Path does not exist" {
				t.Errorf("view failure = %+v", res)
			}
		case "call-edit":
			if !res.IsError || res.Text() != "No match found" {
				t.Errorf("edit failure = %+v", res)
			}
		case "call-fetch":
			// A failed fetch still promotes its retrieval metrics.
			if !res.IsError || res.Fetch == nil || res.Fetch.StatusCode != 404 || res.Fetch.URL != "https://example.com/nope-404" {
				t.Errorf("fetch failure = %+v fetch %+v", res, res.Fetch)
			}
		case "call-img":
			if res.IsError || len(res.Content) != 2 {
				t.Fatalf("image view = %+v", res)
			}
			if res.Content[0].Kind != session.ContentText || res.Content[1].Kind != session.ContentImage ||
				res.Content[1].MimeType != "image/png" || !bytes.Contains(res.Content[1].Raw, []byte(`"assetId"`)) {
				t.Errorf("image blocks = %+v", res.Content)
			}
		}
	}
	if st.SystemBySubtype["session.binary_asset"] != 1 {
		t.Errorf("SystemBySubtype = %v, want one session.binary_asset", st.SystemBySubtype)
	}
	for _, ev := range s.Events {
		if ev.Kind == session.KindSystem && ev.System.Subtype == "session.binary_asset" && !bytes.Contains(ev.System.Details, []byte(`"data":"iVBOR`)) {
			t.Error("binary asset bytes must be preserved in details")
		}
	}
}

// The skills fixture models a project skill invocation (skill.* records),
// the sql tools, the documentation tool, a user-configured MCP server's
// tool, and a --agent custom agent's subagent.selected on the main agent.
func TestSkillsFixture(t *testing.T) {
	s := accounted(t, "skills.jsonl")
	st := s.Stats()
	if st.ToolCalls != 6 || st.ToolErrors != 0 || st.UnansweredCalls != 0 {
		t.Errorf("stats = calls %d errors %d unanswered %d", st.ToolCalls, st.ToolErrors, st.UnansweredCalls)
	}
	for name, kind := range map[string]session.ToolKind{
		"skill": session.ToolKindOther, "sql": session.ToolKindOther, "session_store_sql": session.ToolKindOther,
		"fetch_copilot_cli_documentation": session.ToolKindOther, "everything-echo": session.ToolKindOther, "create": session.ToolKindEdit,
	} {
		if st.ToolCallsByName[name] != 1 {
			t.Errorf("ToolCallsByName[%s] = %d, want 1", name, st.ToolCallsByName[name])
		}
		for _, ti := range s.ToolInteractions() {
			if ti.Call.ToolCall.Name == name && ti.Call.ToolCall.Kind != kind {
				t.Errorf("kind[%s] = %q, want %q", name, ti.Call.ToolCall.Kind, kind)
			}
		}
	}
	for subtype, n := range map[string]int{"skill.invoked": 1, "skill.context_delivered_ref": 1, "subagent.selected": 1} {
		if st.SystemBySubtype[subtype] != n {
			t.Errorf("SystemBySubtype[%s] = %d, want %d", subtype, st.SystemBySubtype[subtype], n)
		}
	}
	for _, ev := range s.Events {
		if ev.AgentID != "" {
			t.Errorf("--agent runs on the main agent; event %s has agent id %q", ev.Kind, ev.AgentID)
		}
		if ev.Kind == session.KindSystem && ev.System.Subtype == "skill.invoked" && !bytes.Contains(ev.System.Details, []byte("# Greeter")) {
			t.Error("skill content must be preserved in details")
		}
	}
}

// The advanced subagents fixture models two task agents in parallel
// (records interleaved), a background agent driven through
// list_agents/read_agent/write_agent across two turns, a general-purpose
// agent that delegates to a nested task agent (subagent.started.parentId),
// and a custom agent from .github/agents.
func TestSubagentsAdvancedFixture(t *testing.T) {
	s := accounted(t, "subagents_advanced.jsonl")

	agents := map[string]int{}
	for _, ev := range s.Events {
		agents[ev.AgentID]++
	}
	for _, id := range []string{"", "agent-one", "agent-two", "agent-bg", "agent-nest", "agent-nest-child", "agent-custom"} {
		if agents[id] == 0 {
			t.Errorf("no events for agent %q", id)
		}
	}
	if len(agents) != 7 {
		t.Errorf("agents = %v, want parent + 6", agents)
	}

	// Every subagent bash call pairs with its own result despite the
	// interleaving, and every delegation is answered.
	for _, ti := range s.ToolInteractions() {
		if ti.Call == nil {
			t.Errorf("orphan result %+v", ti.Results[0].ToolResult)
			continue
		}
		if len(ti.Results) != 1 {
			t.Errorf("call %s (%s) has %d results", ti.Call.ToolCall.ToolCallID, ti.Call.ToolCall.Name, len(ti.Results))
		}
		if ti.Call.AgentID != ti.Results[0].AgentID {
			t.Errorf("call %s agent %q, result agent %q", ti.Call.ToolCall.ToolCallID, ti.Call.AgentID, ti.Results[0].AgentID)
		}
	}
	st := s.Stats()
	if st.ToolCallsByName["task"] != 6 || st.ToolCallsByName["read_agent"] != 2 || st.ToolCallsByName["write_agent"] != 1 || st.ToolCallsByName["list_agents"] != 1 || st.ToolCallsByName["bash"] != 6 {
		t.Errorf("ToolCallsByName = %v", st.ToolCallsByName)
	}
	for _, ti := range s.ToolInteractions() {
		if ti.Call.ToolCall.Name == "read_agent" && ti.Call.ToolCall.Kind != session.ToolKindRead {
			t.Errorf("read_agent kind = %q", ti.Call.ToolCall.Kind)
		}
	}
	if st.UserMessages != 1 || st.HarnessMessages != 7 {
		t.Errorf("user %d harness %d, want 1 and 7 (six delegations plus the background agent's second turn)", st.UserMessages, st.HarnessMessages)
	}
	if st.SystemBySubtype["subagent.started"] != 6 || st.SystemBySubtype["subagent.completed"] != 6 {
		t.Errorf("lifecycle = started %d completed %d, want 6 each (the background agent's second turn has no bracket)", st.SystemBySubtype["subagent.started"], st.SystemBySubtype["subagent.completed"])
	}
	// Parent-only view: filter by empty agent id.
	var parentCalls int
	for _, ev := range s.Events {
		if ev.Kind == session.KindToolCall && ev.AgentID == "" {
			parentCalls++
		}
	}
	if parentCalls != 9 {
		t.Errorf("parent tool calls = %d, want 9", parentCalls)
	}
	// The nested child's started record names its parent agent.
	for _, ev := range s.Events {
		if ev.Kind == session.KindSystem && ev.System.Subtype == "subagent.started" && ev.AgentID == "agent-nest-child" && !bytes.Contains(ev.System.Details, []byte(`"parentId":"agent-nest"`)) {
			t.Errorf("nested started = %s", ev.System.Details)
		}
	}
}
