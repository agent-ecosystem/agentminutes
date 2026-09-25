package copilot

import (
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"os"
	"path/filepath"
	"strings"

	"github.com/agent-ecosystem/agentminutes/harness"
)

// Transcript layout under the root (~/.copilot/session-state, or
// $COPILOT_HOME/session-state when the CLI's home override is set): one
// directory per session, named by the session UUID, holding the transcript
// at events.jsonl plus sidecars (workspace.yaml with the cwd and first
// prompt; checkpoints/, files/, research/, rewind-file-snapshots/; a
// .workspace-fork.lock). The root also holds a .session-operation-locks/
// directory of per-session lock files, and the parent ~/.copilot keeps a
// SQLite index (session-store.db) that is not consulted. Sessions are keyed
// by UUID, not by working directory: the cwd is in-band (session.start),
// so a cwd filter is a scan over identities. The transcript records its
// session ID in-band; the directory name is only a fallback when a head
// lacks session.start.

// transcriptName is the transcript's file name inside a session directory.
const transcriptName = "events.jsonl"

// DefaultRoot implements harness.Locator.
func (Adapter) DefaultRoot() (string, error) {
	if home := os.Getenv("COPILOT_HOME"); home != "" {
		return filepath.Join(home, "session-state"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".copilot", "session-state"), nil
}

// Scan implements harness.Locator.
func (a Adapter) Scan(root string, opts harness.ScanOptions) iter.Seq2[harness.SessionRef, error] {
	return func(yield func(harness.SessionRef, error) bool) {
		entries, err := os.ReadDir(root)
		if err != nil {
			yield(harness.SessionRef{}, scanError(root, err))
			return
		}
		for _, e := range entries {
			dir := filepath.Join(root, e.Name())
			switch {
			case !e.IsDir():
				opts.Skip(dir, "not a session directory")
				continue
			case strings.HasPrefix(e.Name(), "."):
				// .session-operation-locks and any other hidden
				// bookkeeping directory.
				opts.SkipTree(dir, true, "not a session directory")
				continue
			}
			transcript := filepath.Join(dir, transcriptName)
			if _, err := os.Stat(transcript); err != nil {
				opts.SkipTree(dir, true, "session directory without transcript")
				continue
			}
			skipSidecars(dir, transcript, opts)
			ref, include, err := a.ref(transcript, e.Name(), opts)
			if err != nil {
				if !yield(harness.SessionRef{}, err) {
					return
				}
				continue
			}
			if include && !yield(ref, nil) {
				return
			}
		}
	}
}

// ref builds a SessionRef for one session transcript, falling back to the
// layout-derived session ID when the head records none.
func (a Adapter) ref(path, dirName string, opts harness.ScanOptions) (ref harness.SessionRef, include bool, err error) {
	ref, include, err = harness.BuildRef(a, path, opts)
	if include && ref.Meta.SessionID == "" {
		ref.Meta.SessionID = dirName
	}
	return ref, include, err
}

// Locate implements harness.Locator. The session ID is the directory name,
// confirmed against the in-band session.start.
func (a Adapter) Locate(root, sessionID string) (harness.SessionRef, error) {
	if err := harness.CheckSessionID(sessionID); err != nil {
		return harness.SessionRef{}, err
	}
	transcript := filepath.Join(root, sessionID, transcriptName)
	if _, err := os.Stat(transcript); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return harness.SessionRef{}, fmt.Errorf("%s: no transcript for session %q under %s: %w", harness.Copilot, sessionID, root, os.ErrNotExist)
		}
		return harness.SessionRef{}, err
	}
	ref, _, err := a.ref(transcript, sessionID, harness.ScanOptions{})
	if err != nil {
		return harness.SessionRef{}, err
	}
	if err := harness.ConfirmSessionID(harness.Copilot, transcript, ref.Meta.SessionID, sessionID); err != nil {
		return harness.SessionRef{}, err
	}
	return ref, nil
}

// Gather implements harness.Locator. Copilot writes a subagent's
// conversation into the parent transcript (every record stamped with an
// agentId), so there are no subagent files: the task is the parent alone,
// and the per-agent split is Event.AgentID.
func (Adapter) Gather(_ string, parent harness.SessionRef) (harness.Task, error) {
	return harness.Task{Parent: parent, Join: harness.JoinInline}, nil
}

func scanError(path string, err error) error {
	return &harness.ScanError{Harness: harness.Copilot, Path: path, Err: err}
}

// skipSidecars reports every regular file in a session directory other
// than the transcript itself. The walk only happens when a listener is set.
func skipSidecars(dir, transcript string, opts harness.ScanOptions) {
	if opts.OnSkip == nil {
		return
	}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && p != transcript {
			opts.OnSkip(p, "session sidecar")
		}
		return nil
	})
}
