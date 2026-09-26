package codex

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/textaudit"
)

// nonText enumerates the (subtype, path) pairs at which a system event's
// Details carry a long string that is not surfaced as Text, with the
// reason; see textaudit. "Decision pending" entries await a
// representation decision and are listed so the omission is explicit.
var nonText = map[string]string{
	"item_completed item.aggregated_output":                              "the item stream echoes the exec output already on the tool_result",
	"item_completed item.content.[].text":                                "the item stream echoes message text already on the user/assistant event",
	"item_completed item.cwd":                                            "a path",
	"item_completed item.formatted_output":                               "the item stream echoes the exec output already on the tool_result",
	"item_completed item.stdout":                                         "the item stream echoes the exec output already on the tool_result",
	"agent_message content.[].encrypted_content":                         "the provider's opaque reasoning blob, not text",
	"session_meta/base_instructions cwd":                                 "a path (details is the whole session_meta payload)",
	"session_meta/base_instructions dynamic_tools.[].description":        "tool definitions, sent as the API's tools parameter, not as prompt text",
	"session_meta/base_instructions runtime_workspace_roots.[]":          "paths",
	"patch_apply_end stdout":                                             "promotable telemetry: codex.PromotePatchApply makes it the tool result",
	"session_meta base_instructions.text":                                "surfaced as Text by the session_meta/base_instructions event emitted from the same record",
	"session_meta cwd":                                                   "a path (a later session_meta; the first becomes the meta event)",
	"session_meta runtime_workspace_roots.[]":                            "paths",
	"task_complete last_agent_message":                                   "echoes the turn's agent_message",
	"thread_settings_applied thread_settings.cwd":                        "a path",
	"thread_settings_applied thread_settings.runtime_workspace_roots.[]": "paths",
	"turn_context collaboration_mode.settings.developer_instructions":    "the turn's developer instructions, delivered to the model as message/developer events (verified on 0.118), which are the surfaced text",
	"turn_context cwd":                                                   "a path",
	"turn_context developer_instructions":                                "the turn's developer instructions, delivered as message/developer events, which are the surfaced text",
	"turn_context file_system_sandbox_policy.entries.[].path.path":       "sandbox paths",
	"turn_context permission_profile.file_system.entries.[].path.path":   "sandbox paths",
	"turn_context workspace_roots.[]":                                    "paths",
	"web_search_end action.query":                                        "promotable telemetry: codex.PromoteWebSearch makes it the tool call input",
	"web_search_end action.url":                                          "promotable telemetry: codex.PromoteWebSearch makes it the tool call input",
	"web_search_end query":                                               "promotable telemetry: codex.PromoteWebSearch makes it the tool call input",
	"world_state state.environments.environments.local.cwd":              "a path",
	"world_state state.environments.filesystem":                          "the environment snapshot the environment_context developer message is rendered from; that message is the surfaced text",
	"world_state state.host_skills.body":                                 "the skills listing the skills_instructions developer message is rendered from; that message is the surfaced text",
}

// TestFixturesTextAudit runs the text-surfacing invariant over every
// fixture.
func TestFixturesTextAudit(t *testing.T) {
	names, err := filepath.Glob(filepath.Join("testdata", "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		s, err := harness.Parse(Adapter{}, bytes.NewReader(data), harness.Options{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		textaudit.Invariant(t, name, s.Events, nonText)
	}
}
