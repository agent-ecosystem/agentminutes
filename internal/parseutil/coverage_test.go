package parseutil

import (
	"slices"
	"testing"

	"github.com/agent-ecosystem/agentminutes/session"
)

func TestUncoveredLines(t *testing.T) {
	data := []byte("{\"a\":1}\n{\"b\":2}\n\n{\"c\":3}\n")
	events := []session.Event{{Provenance: &session.Provenance{Line: 1, EndLine: 2}}}

	if got := UncoveredLines(data, events, []int{4}); got != nil {
		t.Errorf("uncovered = %v, want none (blank line 3 needs no coverage)", got)
	}
	if got := UncoveredLines(data, events, nil); !slices.Equal(got, []int{4}) {
		t.Errorf("uncovered = %v, want [4]", got)
	}
	if got := UncoveredLines(data, nil, nil); !slices.Equal(got, []int{1, 2, 4}) {
		t.Errorf("uncovered = %v, want [1 2 4]", got)
	}
}
