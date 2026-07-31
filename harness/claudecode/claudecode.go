// Package claudecode parses Claude Code's native session transcripts
// (~/.claude/projects/<project>/*.jsonl, including agent-*.jsonl subagent
// transcripts) into the unified schema.
//
// The format rules implemented here were derived empirically from real
// transcripts; see plans/claude-code-format-inventory.md in the repository
// root for the inventory and the normalization rules it fixed.
package claudecode

import (
	"bytes"
	"encoding/json"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/parseutil"
	"github.com/agent-ecosystem/agentminutes/session"
)

// Adapter parses Claude Code transcripts. The zero value is ready to use.
type Adapter struct{}

var _ harness.Adapter = Adapter{}

// ID implements harness.Adapter.
func (Adapter) ID() harness.ID { return harness.ClaudeCode }

// conversationTypes are record types carrying model-visible content; they
// become events.
var conversationTypes = map[string]bool{
	"user":       true,
	"assistant":  true,
	"system":     true,
	"attachment": true,
}

// skipTypes are harness/UI bookkeeping records with no model-visible
// content. They are excluded from the event stream and reported via
// Options.OnSkip, never silently dropped.
var skipTypes = map[string]bool{
	"mode":                  true,
	"permission-mode":       true,
	"ai-title":              true,
	"last-prompt":           true,
	"file-history-snapshot": true,
	"file-history-delta":    true,
	"queue-operation":       true,
}

// toolKinds maps Claude Code's native tool names onto ACP tool kinds.
// Unlisted names (MCP tools, new built-ins) classify as ToolKindOther.
var toolKinds = map[string]session.ToolKind{
	"Read":         session.ToolKindRead,
	"Glob":         session.ToolKindSearch,
	"Grep":         session.ToolKindSearch,
	"ToolSearch":   session.ToolKindSearch,
	"Bash":         session.ToolKindExecute,
	"Edit":         session.ToolKindEdit,
	"Write":        session.ToolKindEdit,
	"NotebookEdit": session.ToolKindEdit,
	"WebFetch":     session.ToolKindFetch,
	"WebSearch":    session.ToolKindFetch,
	"TodoWrite":    session.ToolKindThink,
}

func kindFor(name string) session.ToolKind {
	if k, ok := toolKinds[name]; ok {
		return k
	}
	return session.ToolKindOther
}

// legacyTypes are record types observed only in older Claude Code
// transcripts (see the compaction/summary notes in the format inventory).
// They mark a plausible header but never a certain one: the parser has no
// verified mapping for them.
var legacyTypes = map[string]bool{
	"summary": true,
}

// Sniff implements harness.Adapter. It inspects the first line of the
// header for Claude Code's distinctive record envelope. An unrecognized
// record type grades Possible only alongside an envelope marker key —
// other harnesses' records also carry a "type" field, and a bare one must
// not claim their transcripts.
func (Adapter) Sniff(header []byte) harness.Confidence {
	header = parseutil.TrimBOM(header)
	line, _, _ := bytes.Cut(header, []byte("\n"))
	line = bytes.TrimSpace(line)
	var probe struct {
		Type      string `json:"type"`
		SessionID string `json:"sessionId"`
		UUID      string `json:"uuid"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		// The header may cut off mid-line; fall back to marker fields
		// that only Claude Code's envelope uses together.
		if bytes.Contains(header, []byte(`"parentUuid"`)) &&
			bytes.Contains(header, []byte(`"sessionId"`)) {
			return harness.Possible
		}
		return harness.NoMatch
	}
	switch {
	case conversationTypes[probe.Type] && probe.SessionID != "" && probe.UUID != "":
		return harness.Certain
	case skipTypes[probe.Type] && (probe.SessionID != "" ||
		probe.Type == "file-history-snapshot" || probe.Type == "file-history-delta"):
		return harness.Certain
	case conversationTypes[probe.Type] || skipTypes[probe.Type] || legacyTypes[probe.Type]:
		return harness.Possible
	case probe.Type != "" && (probe.SessionID != "" ||
		bytes.Contains(line, []byte(`"parentUuid"`)) ||
		bytes.Contains(line, []byte(`"leafUuid"`))):
		return harness.Possible
	default:
		return harness.NoMatch
	}
}
