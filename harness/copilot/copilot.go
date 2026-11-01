// Package copilot parses GitHub Copilot CLI's native session transcripts
// (~/.copilot/session-state/<session-id>/events.jsonl) into the unified
// schema.
//
// The format rules implemented here were derived empirically from real
// transcripts written by Copilot CLI 1.0.88; see
// plans/copilot-format-inventory.md in the repository root. The event
// schema is observed only (GitHub documents the config directory, not the
// events file), so regenerate ground-truth transcripts before extending
// this adapter.
package copilot

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/parseutil"
	"github.com/agent-ecosystem/agentminutes/session"
)

// Adapter parses Copilot CLI events transcripts. The zero value is ready
// to use.
type Adapter struct{}

var _ harness.Adapter = Adapter{}

// ID implements harness.Adapter.
func (Adapter) ID() harness.ID { return harness.Copilot }

// recordTypes are the event types observed on 1.0.88 through 1.0.105.
// Every one of them becomes an event: conversation types map to their
// event kinds, and the rest are preserved as system events with the
// payload in Details. There is no skip list; nothing in the format
// byte-duplicates another record. Subagent conversations (task,
// search_code_subagent) are interleaved in the parent transcript,
// bracketed by subagent.* records and stamped with an agentId envelope
// key that becomes Event.AgentID. The hook.*, external_tool.*, and
// session.compaction_* types come from interactive 1.0.92+ sessions in the
// local corpus (hooks and IDE tools have no headless probe); the model.*
// namespace is the harness's log of its helper-model calls (session title
// and task labelling on a utility model), observed on 1.0.88, and never
// the conversation, which assistant.message records.
var recordTypes = map[string]bool{
	"abort":                             true,
	"assistant.message":                 true,
	"assistant.turn_end":                true,
	"assistant.turn_start":              true,
	"external_tool.completed":           true,
	"external_tool.requested":           true,
	"hook.end":                          true,
	"hook.start":                        true,
	"model.captured_assignment_context": true,
	"model.message":                     true,
	"model.messages_snapshot":           true,
	"model.model_call_started":          true,
	"model.model_call_success":          true,
	"model.response":                    true,
	"model.turn_ended":                  true,
	"model.turn_started":                true,
	"permission.completed":              true,
	"permission.requested":              true,
	"session.binary_asset":              true,
	"session.compaction_complete":       true,
	"session.compaction_start":          true,
	"session.info":                      true,
	"session.model_change":              true,
	"session.permissions_changed":       true,
	"session.resume":                    true,
	"session.shutdown":                  true,
	"session.start":                     true,
	"session.usage_checkpoint":          true,
	"session.usage_record":              true,
	"skill.context_delivered_ref":       true,
	"skill.invoked":                     true,
	"skill.invoked_ref":                 true,
	"subagent.completed":                true,
	"subagent.configured":               true,
	"subagent.deselected":               true,
	"subagent.selected":                 true,
	"subagent.started":                  true,
	"system.message":                    true,
	"tool.execution_complete":           true,
	"tool.execution_start":              true,
	"user.message":                      true,
}

// telemetryPrefixes name the event namespaces that carry harness
// bookkeeping rather than conversation: an unrecognized type under one of
// them is preserved as a system event with its payload in full instead of
// failing the parse (the Codex event_msg precedent). Unknown types in the
// conversation namespaces (assistant., user., tool.) and unknown top-level
// types stay loud. external_tool. (IDE-hosted tool requests, mirrored by
// the ordinary tool records), hook. (hook runs; a hook's additionalContext
// output is model-visible and surfaces as the hook.end event's Text), and
// model. (helper-model calls) joined the list with the 1.0.105 validation.
var telemetryPrefixes = []string{"external_tool.", "hook.", "model.", "permission.", "session.", "skill.", "subagent."}

func isTelemetryType(t string) bool {
	for _, p := range telemetryPrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// toolKinds maps Copilot's built-in tool names onto ACP tool kinds,
// including the search subagent's constrained toolset (file_search,
// grep_search, read_file) and read_agent (reading a background agent's
// replies, the TaskOutput precedent), plus the rg, apply_patch, and
// tool_search_tool names interactive 1.0.92+ sessions record. Unlisted
// names (task, search_code_subagent, list_bash, list_agents, write_agent,
// skill, sql, session_store_sql, fetch_copilot_cli_documentation, the IDE
// sessions' runTests and add_artifact_or_reference, MCP tools such as
// github-mcp-server-* or <server>-<tool>) classify as ToolKindOther.
var toolKinds = map[string]session.ToolKind{
	"apply_patch":      session.ToolKindEdit,
	"bash":             session.ToolKindExecute,
	"create":           session.ToolKindEdit,
	"edit":             session.ToolKindEdit,
	"file_search":      session.ToolKindSearch,
	"glob":             session.ToolKindSearch,
	"grep":             session.ToolKindSearch,
	"grep_search":      session.ToolKindSearch,
	"read_agent":       session.ToolKindRead,
	"read_bash":        session.ToolKindRead,
	"read_file":        session.ToolKindRead,
	"rg":               session.ToolKindSearch,
	"stop_bash":        session.ToolKindExecute,
	"tool_search_tool": session.ToolKindSearch,
	"view":             session.ToolKindRead,
	"web_fetch":        session.ToolKindFetch,
}

func kindFor(name string) session.ToolKind {
	if k, ok := toolKinds[name]; ok {
		return k
	}
	return session.ToolKindOther
}

// Sniff implements harness.Adapter. It inspects the first line for the
// events envelope: {"type": ..., "data": {...}, "id": ..., "timestamp":
// ..., "parentId": ...}, whose leading session.start record names the
// producer and the Copilot version.
func (Adapter) Sniff(header []byte) harness.Confidence {
	header = parseutil.TrimBOM(header)
	line, _, _ := bytes.Cut(header, []byte("\n"))
	var probe struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		Timestamp string          `json:"timestamp"`
		ParentID  json.RawMessage `json:"parentId"`
		Data      struct {
			SessionID      string `json:"sessionId"`
			Producer       string `json:"producer"`
			CopilotVersion string `json:"copilotVersion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(line), &probe); err != nil {
		// The header may cut off mid-line; fall back to markers only the
		// session.start payload carries together.
		if bytes.Contains(header, []byte(`"copilotVersion"`)) &&
			bytes.Contains(header, []byte(`"producer"`)) {
			return harness.Possible
		}
		return harness.NoMatch
	}
	switch {
	case probe.Type == "session.start" && probe.Data.SessionID != "" &&
		(probe.Data.Producer == "copilot-agent" || probe.Data.CopilotVersion != ""):
		return harness.Certain
	case recordTypes[probe.Type] && probe.ID != "" && probe.Timestamp != "" && len(probe.ParentID) > 0:
		// A known type with the full envelope (parentId present, even if
		// null) but not the leading record: plausible, e.g. a truncated
		// or tail-only copy.
		return harness.Possible
	default:
		return harness.NoMatch
	}
}
