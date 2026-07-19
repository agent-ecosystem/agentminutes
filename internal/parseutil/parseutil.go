// Package parseutil holds small helpers shared by the harness adapters.
package parseutil

import (
	"bufio"
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"time"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/session"
)

// MaxLineBytes bounds a single transcript line across every adapter; line
// scanners grow their buffer on demand up to this cap. Tool results and
// file-history snapshots can be very large. Anything else reading
// transcript lines (drift scanning) must use the same cap, or a line an
// adapter accepts would fail elsewhere.
const MaxLineBytes = 256 << 20

// scanBufferSize is a line scanner's initial buffer size.
const scanBufferSize = 64 * 1024

// utf8BOM is tolerated at the start of a transcript (e.g. a file re-saved
// by a Windows editor).
var utf8BOM = []byte("\xef\xbb\xbf")

// TrimBOM strips a leading UTF-8 byte-order mark.
func TrimBOM(b []byte) []byte { return bytes.TrimPrefix(b, utf8BOM) }

// LineScanner reads transcript lines with the conventions every adapter
// shares: buffer growth up to MaxLineBytes, a tolerated UTF-8 BOM on the
// first line, surrounding whitespace (including CRLF) trimmed, and blank
// lines skipped while still counting toward line numbers.
type LineScanner struct {
	sc   *bufio.Scanner
	line int
	data []byte
}

// NewLineScanner returns a LineScanner reading from r.
func NewLineScanner(r io.Reader) *LineScanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, scanBufferSize), MaxLineBytes)
	return &LineScanner{sc: sc}
}

// Scan advances to the next non-blank line, reporting whether one exists.
func (s *LineScanner) Scan() bool {
	for s.sc.Scan() {
		s.line++
		data := s.sc.Bytes()
		if s.line == 1 {
			data = TrimBOM(data)
		}
		data = bytes.TrimSpace(data)
		if len(data) == 0 {
			continue
		}
		s.data = data
		return true
	}
	s.data = nil
	return false
}

// Line returns the 1-based number of the current line (the last physical
// line read, once Scan has returned false).
func (s *LineScanner) Line() int { return s.line }

// Bytes returns the current line, trimmed. Like bufio.Scanner, the slice
// is only valid until the next Scan; clone to retain.
func (s *LineScanner) Bytes() []byte { return s.data }

// Err returns the first error the underlying scanner encountered, if any.
func (s *LineScanner) Err() error { return s.sc.Err() }

// Emitter owns the yield-side plumbing every adapter parser shares:
// emitting events, failing loudly as *harness.ParseError, preserving
// unclassifiable records in permissive mode, and the provenance and
// truncation conventions. Adapters embed it in their parser and keep Line
// (and Version, once the transcript reveals one) current while scanning.
type Emitter struct {
	Harness harness.ID
	Opts    harness.Options
	Yield   func(session.Event, error) bool

	// Line is the 1-based source line of the record being parsed.
	Line int

	// Version is the harness version, once discovered.
	Version string
}

// Emit yields a normal event.
func (e *Emitter) Emit(ev session.Event) bool { return e.Yield(ev, nil) }

// Fail yields a *harness.ParseError carrying the harness, version, and
// line context, and always returns false.
func (e *Emitter) Fail(msg string, err error) bool {
	e.Yield(session.Event{}, &harness.ParseError{
		Harness:        e.Harness,
		HarnessVersion: cmp.Or(e.Version, e.Opts.HarnessVersionHint),
		Line:           e.Line,
		Msg:            msg,
		Err:            err,
	})
	return false
}

// Unknown preserves an unclassifiable record as a KindUnknown event.
func (e *Emitter) Unknown(recordType string, data []byte, reason string) bool {
	return e.Emit(session.Event{
		Kind:       session.KindUnknown,
		Provenance: &session.Provenance{Line: e.Line, EndLine: e.Line},
		Unknown: &session.Unknown{
			RecordType: recordType,
			Reason:     reason,
			Raw:        CloneRaw(data),
		},
	})
}

// UnknownOrFail handles an unclassifiable record: a loud parse error by
// default, or a preserved unknown event in permissive mode.
func (e *Emitter) UnknownOrFail(recordType string, data []byte, reason string) bool {
	if !e.Opts.Permissive {
		return e.Fail(reason, nil)
	}
	return e.Unknown(recordType, data, reason)
}

// Prov is single-record provenance for the current line, retaining the
// verbatim record when KeepRaw is set.
func (e *Emitter) Prov(data []byte) *session.Provenance {
	pr := &session.Provenance{Line: e.Line, EndLine: e.Line}
	if e.Opts.KeepRaw {
		pr.Raw = []json.RawMessage{CloneRaw(data)}
	}
	return pr
}

// Truncate applies Opts.MaxPayloadBytes to a tool result.
func (e *Emitter) Truncate(tr *session.ToolResult) { Truncate(tr, e.Opts.MaxPayloadBytes) }

// ParseBlocks decodes API message content, which is either a bare string
// (one text block) or an array of typed blocks. fromText builds a block
// from a bare string; setRaw retains each array element verbatim on its
// decoded block.
func ParseBlocks[T any](raw json.RawMessage, fromText func(string) T, setRaw func(*T, json.RawMessage)) ([]T, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		return []T{fromText(s)}, nil
	}
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return nil, err
	}
	blocks := make([]T, 0, len(elems))
	for _, e := range elems {
		var b T
		if err := json.Unmarshal(e, &b); err != nil {
			return nil, err
		}
		setRaw(&b, e)
		blocks = append(blocks, b)
	}
	return blocks, nil
}

// CloneRaw copies b into an independent RawMessage, safe to retain after a
// scanner reuses its buffer.
func CloneRaw(b []byte) json.RawMessage { return json.RawMessage(bytes.Clone(b)) }

// Digest returns the SHA-256 hex digest of b.
func Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ParseTime parses an RFC3339 timestamp, returning nil on absence or
// malformation (timestamps are extension data; they never fail a parse).
func ParseTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}

// Truncate applies a max-payload limit to a tool result, replacing
// oversized payloads with size-and-digest placeholders per the schema's
// truncation contract. A limit of zero or less keeps payloads whole.
// Content blocks carry either Text or Raw by construction (see the
// adapters' toContentBlock mappings); if a block ever carried both
// oversized, Size and Digest would describe the Raw payload.
func Truncate(tr *session.ToolResult, limit int64) {
	if limit <= 0 {
		return
	}
	for i := range tr.Content {
		b := &tr.Content[i]
		if int64(len(b.Text)) > limit {
			b.Truncated = true
			b.Size = int64(len(b.Text))
			b.Digest = Digest([]byte(b.Text))
			b.Text = ""
		}
		if int64(len(b.Raw)) > limit {
			b.Truncated = true
			b.Size = int64(len(b.Raw))
			b.Digest = Digest(b.Raw)
			b.Raw = nil
		}
	}
	if int64(len(tr.Enrichment)) > limit {
		placeholder, err := json.Marshal(map[string]any{
			"truncated": true,
			"size":      len(tr.Enrichment),
			"sha256":    Digest(tr.Enrichment),
		})
		if err == nil {
			tr.Enrichment = placeholder
		}
	}
}
