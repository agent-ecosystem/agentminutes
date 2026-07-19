package claudecode

import (
	"iter"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/agent-ecosystem/agentminutes/harness"
)

// Transcript layout under the root (~/.claude/projects): one directory per
// working directory (path-encoded name, lossy: both "/" and "." become "-",
// so cwd matching must use the in-band value, never the directory name),
// session transcripts at <project>/<session-id>.jsonl, and subagent
// transcripts at <project>/<session-id>/subagents/agent-*.jsonl.

// DefaultRoot implements harness.Locator.
func (Adapter) DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// Scan implements harness.Locator.
func (a Adapter) Scan(root string, opts harness.ScanOptions) iter.Seq2[harness.SessionRef, error] {
	return func(yield func(harness.SessionRef, error) bool) {
		projects, err := os.ReadDir(root)
		if err != nil {
			yield(harness.SessionRef{}, scanError(root, err))
			return
		}
		for _, proj := range projects {
			path := filepath.Join(root, proj.Name())
			if !proj.IsDir() {
				opts.Skip(path, "not a project directory")
				continue
			}
			if !a.scanProject(path, opts, yield) {
				return
			}
		}
	}
}

// scanProject enumerates one project directory. Session files and session
// directories pair by stem; a session directory without its transcript
// yields its subagent files as standalone refs rather than dropping them.
func (a Adapter) scanProject(dir string, opts harness.ScanOptions, yield func(harness.SessionRef, error) bool) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return yield(harness.SessionRef{}, scanError(dir, err))
	}
	sessions := make(map[string]string) // stem -> transcript path
	dirs := make(map[string]string)     // stem -> session directory path
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		switch {
		case e.IsDir():
			dirs[e.Name()] = path
		case strings.HasSuffix(e.Name(), ".jsonl"):
			sessions[strings.TrimSuffix(e.Name(), ".jsonl")] = path
		default:
			opts.Skip(path, "not a session transcript")
		}
	}
	for _, stem := range slices.Sorted(maps.Keys(sessions)) {
		subagents := a.collectSubagents(dirs[stem], opts)
		ref, include, err := a.ref(sessions[stem], subagents, opts)
		if err != nil {
			if !yield(harness.SessionRef{}, err) {
				return false
			}
			continue
		}
		if include && !yield(ref, nil) {
			return false
		}
	}
	for _, stem := range slices.Sorted(maps.Keys(dirs)) {
		if _, ok := sessions[stem]; ok {
			continue
		}
		for _, agent := range a.collectSubagents(dirs[stem], opts) {
			ref, include, err := a.ref(agent, nil, opts)
			if err != nil {
				if !yield(harness.SessionRef{}, err) {
					return false
				}
				continue
			}
			if include && !yield(ref, nil) {
				return false
			}
		}
	}
	return true
}

// collectSubagents lists the subagent transcripts under a session
// directory (which may be ""), reporting everything else in the directory
// as skips when a listener is set.
func (Adapter) collectSubagents(sessionDir string, opts harness.ScanOptions) []string {
	if sessionDir == "" {
		return nil
	}
	var agents []string
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		opts.Skip(sessionDir, "unreadable session directory")
		return nil
	}
	for _, e := range entries {
		path := filepath.Join(sessionDir, e.Name())
		if e.Name() != "subagents" {
			opts.SkipTree(path, e.IsDir(), "session directory sidecar")
			continue
		}
		subs, err := os.ReadDir(path)
		if err != nil {
			opts.Skip(path, "unreadable subagents directory")
			continue
		}
		for _, s := range subs {
			sp := filepath.Join(path, s.Name())
			if s.IsDir() || !strings.HasPrefix(s.Name(), "agent-") || !strings.HasSuffix(s.Name(), ".jsonl") {
				opts.SkipTree(sp, s.IsDir(), "not a subagent transcript")
				continue
			}
			agents = append(agents, sp)
		}
	}
	slices.Sort(agents)
	return agents
}

// ref builds a SessionRef for one transcript file. include is false when a
// Since/Until window excludes the session.
func (a Adapter) ref(path string, subagents []string, opts harness.ScanOptions) (ref harness.SessionRef, include bool, err error) {
	ref, include, err = harness.BuildRef(a, path, opts)
	if include {
		ref.SubagentPaths = subagents
	}
	return ref, include, err
}

// Locate implements harness.Locator. The transcript filename is the
// session ID, so resolution is a one-level glob confirmed in-band.
func (a Adapter) Locate(root, sessionID string) (harness.SessionRef, error) {
	if err := harness.CheckSessionID(sessionID); err != nil {
		return harness.SessionRef{}, err
	}
	path, err := harness.ResolveGlob(harness.ClaudeCode, root, sessionID, filepath.Join(root, "*", sessionID+".jsonl"))
	if err != nil {
		return harness.SessionRef{}, err
	}
	sessionDir := strings.TrimSuffix(path, ".jsonl")
	if info, err := os.Stat(sessionDir); err != nil || !info.IsDir() {
		sessionDir = ""
	}
	ref, _, err := a.ref(path, a.collectSubagents(sessionDir, harness.ScanOptions{}), harness.ScanOptions{})
	if err != nil {
		return harness.SessionRef{}, err
	}
	if err := harness.ConfirmSessionID(harness.ClaudeCode, path, ref.Meta.SessionID, sessionID); err != nil {
		return harness.SessionRef{}, err
	}
	return ref, nil
}

func scanError(path string, err error) error {
	return &harness.ScanError{Harness: harness.ClaudeCode, Path: path, Err: err}
}
