package codex

import (
	"iter"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/agent-ecosystem/agentminutes/harness"
)

// Transcript layout under the root (~/.codex/sessions): date directories
// YYYY/MM/DD holding rollout-<timestamp>-<session-id>.jsonl files. The scan
// accepts a rollout file at any depth (archives flatten layouts), but
// pruning and Locate assume the native date layout.

// DefaultRoot implements harness.Locator.
func (Adapter) DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex", "sessions"), nil
}

// Scan implements harness.Locator.
func (a Adapter) Scan(root string, opts harness.ScanOptions) iter.Seq2[harness.SessionRef, error] {
	return func(yield func(harness.SessionRef, error) bool) {
		a.scanDir(root, nil, opts, yield)
	}
}

func (a Adapter) scanDir(dir string, dateParts []string, opts harness.ScanOptions, yield func(harness.SessionRef, error) bool) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return yield(harness.SessionRef{}, scanError(dir, err))
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		switch {
		case e.IsDir():
			parts := append(slices.Clone(dateParts), e.Name())
			if len(parts) == 3 && prunable(parts, opts) {
				continue // whole day outside the window; never walked
			}
			if !a.scanDir(path, parts, opts, yield) {
				return false
			}
		case isRollout(e.Name()):
			ref, include, err := harness.BuildRef(a, path, opts)
			if err != nil {
				if !yield(harness.SessionRef{}, err) {
					return false
				}
				continue
			}
			if include && !yield(ref, nil) {
				return false
			}
		default:
			opts.Skip(path, "not a session transcript")
		}
	}
	return true
}

func isRollout(name string) bool {
	return strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, ".jsonl")
}

// prunable reports whether a YYYY/MM/DD directory chain falls entirely
// outside the Since/Until window, with a day of slack on each side: a
// session spanning midnight (or written across timezones) lives in its
// start-date directory, so the exact Includes filter still decides
// membership for everything the pruning lets through.
func prunable(parts []string, opts harness.ScanOptions) bool {
	if opts.Since.IsZero() && opts.Until.IsZero() {
		return false
	}
	y, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	d, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return false // not a date directory; walk it
	}
	day := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.Local)
	const slack = 24 * time.Hour
	if !opts.Since.IsZero() && day.Add(slack+24*time.Hour).Before(opts.Since) {
		return true
	}
	if !opts.Until.IsZero() && day.Add(-slack).After(opts.Until) {
		return true
	}
	return false
}

// Locate implements harness.Locator. Rollout filenames end in the session
// ID, so resolution is a date-layout glob confirmed in-band.
func (a Adapter) Locate(root, sessionID string) (harness.SessionRef, error) {
	if err := harness.CheckSessionID(sessionID); err != nil {
		return harness.SessionRef{}, err
	}
	path, err := harness.ResolveGlob(harness.Codex, root, sessionID, filepath.Join(root, "*", "*", "*", "rollout-*-"+sessionID+".jsonl"))
	if err != nil {
		return harness.SessionRef{}, err
	}
	ref, _, err := harness.BuildRef(a, path, harness.ScanOptions{})
	if err != nil {
		return harness.SessionRef{}, err
	}
	if err := harness.ConfirmSessionID(harness.Codex, path, ref.Meta.SessionID, sessionID); err != nil {
		return harness.SessionRef{}, err
	}
	return ref, nil
}

func scanError(path string, err error) error {
	return &harness.ScanError{Harness: harness.Codex, Path: path, Err: err}
}
