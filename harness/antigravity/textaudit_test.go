package antigravity

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/textaudit"
)

// nonText enumerates the (subtype, path) pairs at which a system event's
// Details carry a long string that is not surfaced as Text, with the
// reason; see textaudit. Empty: every text-bearing record surfaces its
// text, and the corpus audit found nothing else.
var nonText = map[string]string{}

// TestFixturesTextAudit runs the text-surfacing invariant over every
// fixture.
func TestFixturesTextAudit(t *testing.T) {
	names, err := filepath.Glob(filepath.Join("testdata", "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		s, err := harness.Parse(Adapter{}, bytes.NewReader(data), harness.Options{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		textaudit.Invariant(t, name, s.Events, nonText)
	}
}
