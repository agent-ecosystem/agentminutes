package session

// Meta is session identity knowable at parse start. It is emitted as the
// first event of every stream so streaming consumers learn what they are
// reading before EOF; end-of-stream aggregates live on Session instead.
type Meta struct {
	// Harness identifies the source harness (e.g. "claude-code").
	Harness string `json:"harness" schema:"ext"`

	// HarnessVersion is the harness release that wrote the transcript
	// (e.g. "2.1.197"). Required in parse errors; recorded here for
	// consumers.
	HarnessVersion string `json:"harness_version,omitempty" schema:"ext"`

	// SessionID is the harness-native session identifier. For subagent
	// transcripts this is the parent session's ID (matching the native
	// convention); SubagentID identifies the subagent itself.
	SessionID string `json:"session_id,omitempty" schema:"acp"`

	// SubagentID identifies a subagent (sidechain) transcript.
	SubagentID string `json:"subagent_id,omitempty" schema:"ext"`

	// IsSubagent marks transcripts of harness-spawned subagents.
	IsSubagent bool `json:"is_subagent,omitempty" schema:"ext"`

	// CWD is the working directory the session ran in.
	CWD string `json:"cwd,omitempty" schema:"acp"`

	// GitBranch is the checked-out branch, when the harness recorded it.
	GitBranch string `json:"git_branch,omitempty" schema:"ext"`
}
