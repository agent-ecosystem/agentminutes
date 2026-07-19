// Package codex parses Codex CLI's native session transcripts (rollout
// files under ~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl) into the
// unified schema.
//
// The format rules implemented here were derived empirically from real
// transcripts (Codex 0.118 and 0.144); see plans/codex-format-inventory.md
// in the repository root. The rollout format drifts quickly between
// releases; regenerate ground-truth transcripts before extending this
// adapter.
package codex

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/parseutil"
	"github.com/agent-ecosystem/agentminutes/session"
)

// Adapter parses Codex CLI rollout transcripts. The zero value is ready to
// use.
type Adapter struct{}

var _ harness.Adapter = Adapter{}

// ID implements harness.Adapter.
func (Adapter) ID() harness.ID { return harness.Codex }

// recordTypes are the known top-level rollout record types.
var recordTypes = map[string]bool{
	"session_meta":  true,
	"response_item": true,
	"event_msg":     true,
	"turn_context":  true,
	"world_state":   true,
	"compacted":     true,
}

// skipEventMsgTypes are event_msg payloads that byte-duplicate adjacent
// response_item records (or stream deltas of them). They are excluded from
// the event stream and reported via Options.OnSkip under an
// "event_msg/<type>" key, never silently dropped.
var skipEventMsgTypes = map[string]bool{
	"user_message":    true,
	"agent_message":   true,
	"agent_reasoning": true,
}

func skipEventMsg(t string) bool {
	return skipEventMsgTypes[t] || strings.HasSuffix(t, "_delta")
}

// toolKinds maps Codex tool names onto ACP tool kinds. Unlisted names
// (MCP tools, new built-ins) classify as ToolKindOther.
var toolKinds = map[string]session.ToolKind{
	"exec":                 session.ToolKindExecute,
	"shell":                session.ToolKindExecute,
	"local_shell":          session.ToolKindExecute,
	"exec_command":         session.ToolKindExecute,
	"unified_exec":         session.ToolKindExecute,
	"apply_patch":          session.ToolKindEdit,
	"update_plan":          session.ToolKindThink,
	"web_search":           session.ToolKindFetch,
	"view_image":           session.ToolKindRead,
	"read_thread_terminal": session.ToolKindRead,
}

func kindFor(name string) session.ToolKind {
	if k, ok := toolKinds[name]; ok {
		return k
	}
	return session.ToolKindOther
}

// harnessTagPrefixes mark user-role messages that Codex injects itself.
// A human pasting text that starts with exactly these tags would be
// misclassified; accepted and documented.
var harnessTagPrefixes = []string{
	"<environment_context>",
	"<ENVIRONMENT_CONTEXT>",
	"<user_instructions>",
	"<turn_aborted>",
}

// Sniff implements harness.Adapter. It inspects the first line for the
// rollout envelope: {"timestamp": ..., "type": ..., "payload": ...}.
func (Adapter) Sniff(header []byte) harness.Confidence {
	header = parseutil.TrimBOM(header)
	line, _, _ := bytes.Cut(header, []byte("\n"))
	var probe struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Payload   struct {
			ID         string `json:"id"`
			SessionID  string `json:"session_id"`
			CLIVersion string `json:"cli_version"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(line), &probe); err != nil {
		if bytes.Contains(header, []byte(`"payload"`)) &&
			bytes.Contains(header, []byte(`"cli_version"`)) {
			return harness.Possible
		}
		return harness.NoMatch
	}
	switch {
	case probe.Type == "session_meta" &&
		(probe.Payload.SessionID != "" || probe.Payload.ID != "") &&
		probe.Payload.CLIVersion != "":
		return harness.Certain
	case recordTypes[probe.Type] && probe.Timestamp != "":
		return harness.Possible
	default:
		return harness.NoMatch
	}
}
