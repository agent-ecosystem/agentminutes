// Package harness defines the contract that per-harness adapter packages
// (harness/claudecode, harness/codex, ...) implement, and the shared types
// their signatures need. It exists as a leaf package so both the adapters
// and the root facade can import it without a cycle.
package harness

import (
	"fmt"
	"io"
	"iter"

	"github.com/agent-ecosystem/agentminutes/session"
)

// ID identifies a supported harness.
type ID string

// Supported harnesses, alphabetically.
const (
	Antigravity ID = "antigravity"
	ClaudeCode  ID = "claude-code"
	Codex       ID = "codex"
)

// Options control parsing behavior across all adapters.
type Options struct {
	// Permissive converts unclassifiable records into session.KindUnknown
	// events instead of failing the parse. The default (false) errors
	// loudly, per the schema contract: silently dropping events corrupts
	// behavioral metrics.
	Permissive bool

	// KeepRaw retains verbatim source records in each event's Provenance.
	KeepRaw bool

	// MaxPayloadBytes, when positive, replaces tool-result content larger
	// than the limit with a size-and-hash placeholder. Zero means payloads
	// are always kept whole.
	MaxPayloadBytes int64

	// HarnessVersionHint is a caller-supplied harness version for
	// transcripts whose format does not record one (Antigravity records no
	// version anywhere). It is provenance metadata only: adapters stamp it
	// into session_meta and parse errors, and never consult it for parse
	// dispatch. A version the transcript itself records always wins.
	HarnessVersionHint string

	// OnSkip, when set, is called for each native record the adapter
	// excludes per its documented skip list, with the 1-based source line
	// and native record type. Parse wires this into the session report;
	// streaming consumers who care about skip accounting set it themselves.
	OnSkip func(line int, recordType string)
}

// SniffSize is how much of a transcript head format detection may inspect:
// callers feeding Adapter.Sniff (or the facade's Detect) should supply up to
// this many leading bytes, and adapters must not assume more.
const SniffSize = 64 * 1024

// Confidence grades a Sniff match.
type Confidence int

// Confidence levels.
const (
	// NoMatch means the header is not this adapter's format.
	NoMatch Confidence = iota

	// Possible means the header is plausible but not distinctive.
	Possible

	// Certain means the header carries this format's distinctive markers.
	Certain
)

func (c Confidence) String() string {
	switch c {
	case Certain:
		return "certain"
	case Possible:
		return "possible"
	default:
		return "no-match"
	}
}

// Adapter parses one harness's native transcript format into the unified
// schema. Implementations must account for every input record as exactly
// one of: a normalized event, a documented skip (counted in the session
// report), or an error.
type Adapter interface {
	// ID identifies the harness this adapter parses.
	ID() ID

	// Sniff inspects the head of a transcript (at most SniffSize bytes;
	// adapters must not assume more) and grades whether it is this
	// adapter's format.
	Sniff(header []byte) Confidence

	// Events parses the transcript as an ordered event stream. The first
	// event is always session.KindSessionMeta. On error the sequence
	// yields the error and stops; errors identify harness, version, and
	// source line (see ParseError).
	Events(r io.Reader, opts Options) iter.Seq2[session.Event, error]
}

// ParseError is the loud-failure currency of every adapter: it identifies
// what failed to parse, in which harness format and version, and where.
type ParseError struct {
	Harness ID

	// HarnessVersion is the transcript's harness version, when it was
	// discovered before the failure. Schema drift errors must carry it.
	HarnessVersion string

	// Line is the 1-based source line of the failing record, when known.
	Line int

	// Msg says what was unrecognized or malformed.
	Msg string

	// Err is the underlying error, if any.
	Err error
}

func (e *ParseError) Error() string {
	s := fmt.Sprintf("%s: %s", e.Harness, e.Msg)
	if e.HarnessVersion != "" {
		if last := LastValidated(e.Harness); last != "" && VersionNewer(e.HarnessVersion, last) {
			s = fmt.Sprintf("%s (harness version %s, newer than last validated %s; likely format drift, check for an agentminutes update)", s, e.HarnessVersion, last)
		} else {
			s = fmt.Sprintf("%s (harness version %s)", s, e.HarnessVersion)
		}
	}
	if e.Line > 0 {
		s = fmt.Sprintf("%s at line %d", s, e.Line)
	}
	if e.Err != nil {
		s = fmt.Sprintf("%s: %v", s, e.Err)
	}
	return s
}

func (e *ParseError) Unwrap() error { return e.Err }
