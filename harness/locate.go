package harness

import (
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/agent-ecosystem/agentminutes/session"
)

// Locator is the discovery counterpart to Adapter: it knows where a harness
// writes session transcripts on this machine and how to cheaply read
// identity from them. It is a separate contract because Adapter is
// deliberately io.Reader-based; parsing a stream must never require a
// filesystem.
type Locator interface {
	// ID identifies the harness this locator discovers.
	ID() ID

	// DefaultRoot returns the harness's conventional transcript root for
	// the current user (e.g. ~/.claude/projects). It does not check that
	// the directory exists.
	DefaultRoot() (string, error)

	// Scan enumerates session transcripts under root. Every regular file
	// under root is accounted for as exactly one of: part of a yielded ref
	// (main or subagent transcript), a reported skip (ScanOptions.OnSkip),
	// or a yielded *ScanError — except files excluded by a Since/Until
	// window, which are omitted without notice (pruned subtrees are never
	// walked at all; that is the point of the window). Unlike
	// Adapter.Events, a yielded error does not end the sequence: it
	// identifies one expected-transcript file that could not be read or
	// identified, and the scan continues. Skip discovery that requires
	// extra directory walking is only performed when OnSkip is set.
	Scan(root string, opts ScanOptions) iter.Seq2[SessionRef, error]

	// Locate resolves a known session ID under root without a full scan,
	// using layout-level matching (filename or directory name) confirmed
	// by an identity read. The error wraps os.ErrNotExist when no
	// transcript for the ID exists under root.
	Locate(root, sessionID string) (SessionRef, error)
}

// ScanOptions control a Locator.Scan.
type ScanOptions struct {
	// Since and Until, when non-zero, keep only sessions whose activity
	// interval (see Includes) overlaps the window. Locators may use layout
	// knowledge to prune the walk (Codex date directories), so files
	// outside the window are exempt from per-file accounting; scan
	// without a window for full accounting.
	Since, Until time.Time

	// OnSkip, when set, is called once per file excluded per the
	// locator's documented layout rules (non-transcript files, sidecar
	// assets), with the path and a stable reason string.
	OnSkip func(path, reason string)
}

// Skip reports one excluded file when a listener is set. It is the
// reporting helper Locator implementations share.
func (o ScanOptions) Skip(path, reason string) {
	if o.OnSkip != nil {
		o.OnSkip(path, reason)
	}
}

// SkipTree reports path — a single file, or every regular file under it
// when isDir — as skipped. The walk only happens when a listener is set.
func (o ScanOptions) SkipTree(path string, isDir bool, reason string) {
	if o.OnSkip == nil {
		return
	}
	if !isDir {
		o.OnSkip(path, reason)
		return
	}
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			o.OnSkip(p, reason)
		}
		return nil
	})
}

// Includes reports whether a session with the given activity interval
// passes the Since/Until window. The interval runs from started (the first
// in-band timestamp; nil when the head carries none) to modTime, whichever
// order they fall in — an archived copy's mtime can predate its content.
func (o ScanOptions) Includes(started *time.Time, modTime time.Time) bool {
	start, end := modTime, modTime
	if started != nil {
		start = *started
		if end.Before(start) {
			end = start
		}
	}
	if !o.Since.IsZero() && end.Before(o.Since) {
		return false
	}
	if !o.Until.IsZero() && start.After(o.Until) {
		return false
	}
	return true
}

// SessionRef identifies one discovered native session transcript. It is a
// discovery-layer type, not part of the session JSON contract: no schema
// tags, no SchemaVersion coupling.
type SessionRef struct {
	// Meta is the identity read from the transcript head, enriched with
	// layout-derived identity where the format records none in-band
	// (Antigravity's SessionID is the conversation directory name).
	Meta session.Meta `json:"meta"`

	// Path is the main transcript file.
	Path string `json:"path"`

	// SubagentPaths are transcripts of harness-spawned subagents belonging
	// to this session (Claude Code agent-*.jsonl), each a separate parse
	// input. A subagent file whose parent transcript is missing is yielded
	// as its own ref (Meta.IsSubagent set) rather than dropped.
	SubagentPaths []string `json:"subagent_paths,omitempty"`

	// StartedAt is the first in-band timestamp, when the head carries one.
	StartedAt *time.Time `json:"started_at,omitempty"`

	// ModTime is the file's mtime: a proxy for session end that copying
	// or archiving can falsify. No in-band end timestamp is read (that
	// would mean tail-reading every candidate).
	ModTime time.Time `json:"mod_time"`

	// SizeBytes is the main transcript's size.
	SizeBytes int64 `json:"size_bytes"`
}

// ScanError reports one expected-transcript file that a Scan could not
// read or identify. Scans yield it and continue.
type ScanError struct {
	Harness ID
	Path    string
	Err     error
}

func (e *ScanError) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("%s: %v", e.Harness, e.Err)
	}
	return fmt.Sprintf("%s: %s: %v", e.Harness, e.Path, e.Err)
}

func (e *ScanError) Unwrap() error { return e.Err }

// BuildRef stats path, reads its identity through the adapter, and builds
// the SessionRef a Locator yields for it. include is false when the
// Since/Until window excludes the session. Errors are *ScanError. Locators
// with layout-derived identity or subagent transcripts enrich the returned
// ref afterward.
func BuildRef(a Adapter, path string, opts ScanOptions) (ref SessionRef, include bool, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return SessionRef{}, false, &ScanError{Harness: a.ID(), Path: path, Err: err}
	}
	meta, started, err := ReadIdentity(a, path)
	if err != nil {
		return SessionRef{}, false, &ScanError{Harness: a.ID(), Path: path, Err: err}
	}
	if !opts.Includes(started, info.ModTime()) {
		return SessionRef{}, false, nil
	}
	return SessionRef{
		Meta:      meta,
		Path:      path,
		StartedAt: started,
		ModTime:   info.ModTime(),
		SizeBytes: info.Size(),
	}, true, nil
}

// ResolveGlob resolves a session ID through a Locate layout glob and
// enforces exactly one match: zero matches error wrapping os.ErrNotExist,
// more than one is ambiguous.
func ResolveGlob(id ID, root, sessionID, pattern string) (string, error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", err
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("%s: no transcript for session %q under %s: %w", id, sessionID, root, os.ErrNotExist)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("%s: session %q is ambiguous under %s: %v", id, sessionID, root, matches)
	}
}

// ConfirmSessionID rejects a resolved transcript whose in-band session ID
// does not match the requested one.
func ConfirmSessionID(id ID, path, inBand, requested string) error {
	if inBand != requested {
		return fmt.Errorf("%s: %s records session id %q, not %q", id, path, inBand, requested)
	}
	return nil
}

// ReadIdentity reads a transcript's identity through the adapter's own
// Events stream: it returns the leading session_meta event's payload and
// timestamp, then stops reading. Identity thus comes from the same parser
// as a full parse — no second header parser to drift out of sync. The read
// is deliberately uncapped: parsers already bound line size, and the worst
// case (a file whose head yields no meta) is one bounded read that fails
// loudly rather than a legitimate transcript with a large first record
// failing a byte budget.
func ReadIdentity(a Adapter, path string) (session.Meta, *time.Time, error) {
	f, err := os.Open(path)
	if err != nil {
		return session.Meta{}, nil, err
	}
	defer f.Close() //nolint:errcheck // read-only
	return identityFrom(a, f)
}

func identityFrom(a Adapter, r io.Reader) (session.Meta, *time.Time, error) {
	for ev, err := range a.Events(r, Options{}) {
		if err != nil {
			return session.Meta{}, nil, err
		}
		if ev.Kind == session.KindSessionMeta && ev.SessionMeta != nil {
			return *ev.SessionMeta, ev.Timestamp, nil
		}
		// Meta is by contract the first event; anything else here is an
		// adapter bug, but fail loudly rather than assume.
		return session.Meta{}, nil, &ParseError{Harness: a.ID(), Msg: fmt.Sprintf("first event is %q, not session_meta", ev.Kind)}
	}
	return session.Meta{}, nil, &ParseError{Harness: a.ID(), Msg: "no events in transcript"}
}

// CheckSessionID rejects session IDs that could escape a transcript root
// or widen a match when interpolated into layout paths and glob patterns.
// Locate implementations call it before touching the filesystem.
func CheckSessionID(id string) error {
	if id == "" {
		return fmt.Errorf("empty session id")
	}
	if strings.ContainsAny(id, `/\*?[`) || strings.Contains(id, "..") {
		return fmt.Errorf("invalid session id %q", id)
	}
	return nil
}
