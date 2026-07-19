// Package antigravity parses Antigravity CLI (agy) session transcripts
// (~/.gemini/antigravity-cli/brain/<conversation-id>/.system_generated/
// logs/transcript_full.jsonl) into the unified schema.
//
// Parse transcript_full.jsonl, not transcript.jsonl: the record counts
// match but the short variant trims content (notably fetched page text).
// The format rules implemented here were derived empirically from real
// transcripts (agy 1.1.1–1.1.3); see plans/antigravity-format-inventory.md in
// the repository root. Antigravity churns quickly between releases;
// regenerate ground-truth transcripts before extending this adapter.
package antigravity

import (
	"bytes"
	"encoding/json"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/parseutil"
	"github.com/agent-ecosystem/agentminutes/session"
)

// Adapter parses Antigravity CLI transcripts. The zero value is ready to
// use.
type Adapter struct{}

var _ harness.Adapter = Adapter{}

// ID implements harness.Adapter.
func (Adapter) ID() harness.ID { return harness.Antigravity }

// knownSources are the record source values.
var knownSources = map[string]bool{
	"USER_EXPLICIT": true,
	"MODEL":         true,
	"SYSTEM":        true,
}

// resultTypes are step records that are always the outcome of a preceding
// tool call.
var resultTypes = map[string]bool{
	"RUN_COMMAND":      true,
	"VIEW_FILE":        true,
	"LIST_DIRECTORY":   true,
	"GREP_SEARCH":      true,
	"SEARCH_WEB":       true,
	"READ_URL_CONTENT": true,
	"CODE_ACTION":      true,
}

// ambiguousResultTypes are step records that are tool results when a call
// is pending and standalone system activity otherwise (e.g. GENERIC
// carries permission listings with no preceding call).
var ambiguousResultTypes = map[string]bool{
	"GENERIC":      true,
	"ASK_QUESTION": true,
}

// dispatchTypes are the record types the parser's dispatch switch handles
// directly (everything that is neither a result type nor ambiguous). Keep
// in step with parser.record's cases.
var dispatchTypes = map[string]bool{
	"USER_INPUT":           true,
	"CONVERSATION_HISTORY": true,
	"SYSTEM_MESSAGE":       true,
	"PLANNER_RESPONSE":     true,
	"CHECKPOINT":           true,
	"ERROR_MESSAGE":        true,
}

// knownTypes is the full record vocabulary, for sniffing: the union of
// dispatchTypes, resultTypes, and ambiguousResultTypes, so the sniffing
// vocabulary cannot drift from the parsing one.
var knownTypes = func() map[string]bool {
	m := make(map[string]bool)
	for _, part := range []map[string]bool{dispatchTypes, resultTypes, ambiguousResultTypes} {
		for k := range part {
			m[k] = true
		}
	}
	return m
}()

// toolKinds maps Antigravity tool-call names onto ACP tool kinds. Unlisted
// names classify as ToolKindOther.
var toolKinds = map[string]session.ToolKind{
	"run_command":      session.ToolKindExecute,
	"write_to_file":    session.ToolKindEdit,
	"view_file":        session.ToolKindRead,
	"list_dir":         session.ToolKindRead,
	"grep_search":      session.ToolKindSearch,
	"search_web":       session.ToolKindFetch,
	"read_url_content": session.ToolKindFetch,
}

func kindFor(name string) session.ToolKind {
	if k, ok := toolKinds[name]; ok {
		return k
	}
	return session.ToolKindOther
}

// Sniff implements harness.Adapter. It inspects the first line for the
// step-record envelope: {"step_index": ..., "source": ..., "type": ...}.
func (Adapter) Sniff(header []byte) harness.Confidence {
	header = parseutil.TrimBOM(header)
	line, _, _ := bytes.Cut(header, []byte("\n"))
	var probe struct {
		StepIndex *int   `json:"step_index"`
		Source    string `json:"source"`
		Type      string `json:"type"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(line), &probe); err != nil {
		// The header may cut off mid-line (USER_INPUT records are long);
		// fall back to the envelope keys only this format uses together.
		if bytes.Contains(header, []byte(`"step_index"`)) &&
			bytes.Contains(header, []byte(`"source"`)) &&
			bytes.Contains(header, []byte(`"created_at"`)) {
			return harness.Possible
		}
		return harness.NoMatch
	}
	switch {
	case probe.StepIndex != nil && knownSources[probe.Source] && knownTypes[probe.Type]:
		return harness.Certain
	case probe.StepIndex != nil && probe.Source != "" && probe.Type != "":
		return harness.Possible
	default:
		return harness.NoMatch
	}
}
