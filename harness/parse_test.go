package harness

import (
	"io"
	"iter"
	"strings"
	"testing"

	"github.com/agent-ecosystem/agentminutes/session"
)

// stubAdapter yields a fixed event stream and reports one skip on line 3,
// exercising Parse's plumbing without a real transcript format.
type stubAdapter struct{ events []session.Event }

func (stubAdapter) ID() ID                  { return "stub" }
func (stubAdapter) Sniff([]byte) Confidence { return NoMatch }

func (a stubAdapter) Events(_ io.Reader, opts Options) iter.Seq2[session.Event, error] {
	return func(yield func(session.Event, error) bool) {
		if opts.OnSkip != nil {
			opts.OnSkip(3, "stub-skip")
		}
		for _, ev := range a.events {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

func stubMeta() session.Event {
	return session.Event{Kind: session.KindSessionMeta, SessionMeta: &session.Meta{
		Harness: "stub", SessionID: "s-1",
	}}
}

// TestParseSkipChaining pins that Parse both counts skips in the session
// report and still forwards them to a caller-supplied OnSkip.
func TestParseSkipChaining(t *testing.T) {
	var gotLine int
	var gotType string
	opts := Options{OnSkip: func(line int, recordType string) {
		gotLine, gotType = line, recordType
	}}
	s, err := Parse(stubAdapter{events: []session.Event{stubMeta()}}, strings.NewReader(""), opts)
	if err != nil {
		t.Fatal(err)
	}
	if s.Report.SkippedRecords["stub-skip"] != 1 {
		t.Errorf("SkippedRecords = %v, want stub-skip:1", s.Report.SkippedRecords)
	}
	if gotLine != 3 || gotType != "stub-skip" {
		t.Errorf("user OnSkip got (%d, %q), want (3, %q)", gotLine, gotType, "stub-skip")
	}
}

// TestParseTransformOrder pins that transforms apply in argument order: the
// first transform sees the adapter stream, the second wraps the first.
func TestParseTransformOrder(t *testing.T) {
	appendThinking := func(text string) session.Transform {
		return func(events iter.Seq2[session.Event, error]) iter.Seq2[session.Event, error] {
			return func(yield func(session.Event, error) bool) {
				for ev, err := range events {
					if !yield(ev, err) {
						return
					}
				}
				yield(session.Event{Kind: session.KindThinking, Thinking: &session.Thinking{Text: text}}, nil)
			}
		}
	}
	s, err := Parse(stubAdapter{events: []session.Event{stubMeta()}}, strings.NewReader(""), Options{},
		appendThinking("first"), appendThinking("second"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Events) != 3 {
		t.Fatalf("len(Events) = %d, want 3", len(s.Events))
	}
	if s.Events[1].Thinking.Text != "first" || s.Events[2].Thinking.Text != "second" {
		t.Errorf("transform order: got %q then %q, want first then second",
			s.Events[1].Thinking.Text, s.Events[2].Thinking.Text)
	}
}
