package antigravity

import (
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"os"
	"path/filepath"

	"github.com/agent-ecosystem/agentminutes/harness"
)

// Transcript layout under the root (~/.gemini/antigravity-cli/brain): one
// directory per conversation holding the transcript at
// .system_generated/logs/transcript_full.jsonl plus sidecar artifacts
// (step outputs, saved URL content). The transcript records no session ID
// or cwd in-band, so discovery derives the session ID from the
// conversation directory name — the documented out-of-band source the
// parser's sparse meta points at.

// transcriptRel is the transcript's path inside a conversation directory.
var transcriptRel = filepath.Join(".system_generated", "logs", "transcript_full.jsonl")

// DefaultRoot implements harness.Locator.
func (Adapter) DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gemini", "antigravity-cli", "brain"), nil
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
			if !e.IsDir() {
				opts.Skip(dir, "not a conversation directory")
				continue
			}
			transcript := filepath.Join(dir, transcriptRel)
			if _, err := os.Stat(transcript); err != nil {
				opts.SkipTree(dir, true, "conversation directory without transcript")
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

// ref builds a SessionRef for one conversation transcript, enriching the
// parser's deliberately sparse meta with the layout-derived session ID.
// include is false when a Since/Until window excludes the session.
func (a Adapter) ref(path, conversationID string, opts harness.ScanOptions) (ref harness.SessionRef, include bool, err error) {
	ref, include, err = harness.BuildRef(a, path, opts)
	if include {
		ref.Meta.SessionID = conversationID
	}
	return ref, include, err
}

// Locate implements harness.Locator. The session (conversation) ID is the
// directory name; there is nothing in-band to confirm against.
func (a Adapter) Locate(root, sessionID string) (harness.SessionRef, error) {
	if err := harness.CheckSessionID(sessionID); err != nil {
		return harness.SessionRef{}, err
	}
	transcript := filepath.Join(root, sessionID, transcriptRel)
	if _, err := os.Stat(transcript); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return harness.SessionRef{}, fmt.Errorf("antigravity: no transcript for conversation %q under %s: %w", sessionID, root, os.ErrNotExist)
		}
		return harness.SessionRef{}, err
	}
	ref, _, err := a.ref(transcript, sessionID, harness.ScanOptions{})
	return ref, err
}

func scanError(path string, err error) error {
	return &harness.ScanError{Harness: harness.Antigravity, Path: path, Err: err}
}

// skipSidecars reports every regular file in a conversation directory
// other than the transcript itself. The walk only happens when a listener
// is set.
func skipSidecars(dir, transcript string, opts harness.ScanOptions) {
	if opts.OnSkip == nil {
		return
	}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && p != transcript {
			opts.OnSkip(p, "conversation sidecar")
		}
		return nil
	})
}
