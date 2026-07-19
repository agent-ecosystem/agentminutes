package parseutil

import (
	"bytes"

	"github.com/agent-ecosystem/agentminutes/session"
)

// UncoveredLines checks the line-accounting invariant: every non-blank line
// of a transcript must be covered by an event's provenance range or by a
// skip callback. It returns the 1-based lines that are covered by neither,
// in order (nil when the invariant holds). skipLines are the lines reported
// via Options.OnSkip.
func UncoveredLines(data []byte, events []session.Event, skipLines []int) []int {
	covered := make(map[int]bool, len(events)+len(skipLines))
	for _, l := range skipLines {
		covered[l] = true
	}
	for i := range events {
		p := events[i].Provenance
		if p == nil {
			continue
		}
		for l := p.Line; l <= p.EndLine; l++ {
			covered[l] = true
		}
	}
	var uncovered []int
	for i, ln := range bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n")) {
		if len(bytes.TrimSpace(ln)) != 0 && !covered[i+1] {
			uncovered = append(uncovered, i+1)
		}
	}
	return uncovered
}
