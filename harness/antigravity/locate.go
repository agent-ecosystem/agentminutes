package antigravity

import (
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"os"
	"path/filepath"
	"regexp"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/session"
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

// childIDRe matches the conversation ids an invoke_subagent result embeds
// ("Created the following subagents:" followed by JSON with
// conversationId). Nothing structural marks a child conversation, so this
// content join is the only parent-to-child link the format offers.
var childIDRe = regexp.MustCompile(`"conversationId":\s*"([^"]+)"`)

// Gather implements harness.Locator. Children are found by parsing the
// parent and reading the conversation ids out of its invoke_subagent
// results, then located under root; each child is gathered in turn, so
// nested delegation is followed. A child id that does not resolve under
// root is reported in Task.Skipped: the join is content-derived and a
// missing child is a fact about the store, not an error in the parent,
// but the task summary is short by that child.
func (a Adapter) Gather(root string, parent harness.SessionRef) (harness.Task, error) {
	if parent.Meta.SessionID == "" {
		// A bare BuildRef ref lacks the layout-derived conversation id
		// (nothing is recorded in-band); derive it as Scan and Locate do.
		parent.Meta.SessionID = conversationIDOf(parent.Path)
	}
	task := harness.Task{Parent: parent, Join: harness.JoinContent}
	seen := map[string]bool{parent.Meta.SessionID: true}
	var walk func(ref harness.SessionRef) error
	walk = func(ref harness.SessionRef) error {
		ids, err := childIDs(a, ref.Path)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			child, err := a.Locate(root, id)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					task.Skipped = append(task.Skipped, harness.TaskSkip{Path: id, Reason: "child conversation not found under root"})
					continue
				}
				return err
			}
			task.Subagents = append(task.Subagents, child)
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(parent); err != nil {
		return harness.Task{}, err
	}
	return task, nil
}

// conversationIDOf returns the conversation directory name a transcript
// path sits under (<root>/<conversation>/.system_generated/logs/...), or
// "" when the path does not have that layout.
func conversationIDOf(path string) string {
	dir := filepath.Dir(path)
	for range 2 {
		dir = filepath.Dir(dir)
	}
	if rel, err := filepath.Rel(filepath.Dir(dir), path); err == nil && rel == filepath.Join(filepath.Base(dir), transcriptRel) {
		return filepath.Base(dir)
	}
	return ""
}

// childIDs parses a transcript and returns the conversation ids named by
// its invoke_subagent results, in order.
func childIDs(a Adapter, path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, scanError(path, err)
	}
	defer f.Close() //nolint:errcheck // read-only
	var ids []string
	for ev, err := range a.Events(f, harness.Options{}) {
		if err != nil {
			return nil, scanError(path, err)
		}
		if ev.Kind != session.KindToolResult || ev.ToolResult.ToolName != "invoke_subagent" {
			continue
		}
		for _, m := range childIDRe.FindAllStringSubmatch(ev.ToolResult.Text(), -1) {
			ids = append(ids, m[1])
		}
	}
	return ids, nil
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
