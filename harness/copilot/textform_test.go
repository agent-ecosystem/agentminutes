package copilot

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/parseutil"
	"github.com/agent-ecosystem/agentminutes/session"
)

const greeterBody = "# Greeter\n\nWhen asked to greet someone, use the create tool to write greeting.txt containing exactly: hello <name>. Then reply with exactly: greeted\n"

func skillTexts(t *testing.T, data []byte, form harness.TextForm) (invoked, ref string, order []string, events []session.Event) {
	t.Helper()
	var skips []int
	s, err := harness.Parse(Adapter{}, bytes.NewReader(data), harness.Options{
		TextForm: form,
		OnSkip:   func(line int, _ string) { skips = append(skips, line) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if un := parseutil.UncoveredLines(data, s.Events, skips); len(un) > 0 {
		t.Errorf("%d lines not covered (first: %d)", len(un), un[0])
	}
	for _, ev := range s.Events {
		if ev.Kind != session.KindSystem {
			continue
		}
		order = append(order, ev.System.Subtype)
		switch ev.System.Subtype {
		case "skill.invoked":
			invoked = ev.System.Text
		case "skill.context_delivered_ref":
			ref = ev.System.Text
		}
	}
	return invoked, ref, order, s.Events
}

// TestDeliveredTextForm pins Options.TextForm = TextDelivered on the
// skills fixture: skill.invoked's Text is the body inside the prefix and
// suffix of the delivery record that follows it (whose contentId is the
// body's SHA-256), the delivery record itself stays textless, and event
// order and line accounting are unchanged by the one-record hold.
func TestDeliveredTextForm(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "skills.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	bare, _, bareOrder, _ := skillTexts(t, data, harness.TextBare)
	if bare != greeterBody {
		t.Fatalf("bare skill.invoked text = %q", bare)
	}
	got, ref, order, events := skillTexts(t, data, harness.TextDelivered)
	want := "<skill-context name=\"greeter\">\nBase directory for this skill: /tmp/exp/.github/skills/greeter\n\n" + greeterBody + "\n</skill-context>"
	if got != want {
		t.Errorf("delivered skill.invoked text = %q, want %q", got, want)
	}
	if ref != "" {
		t.Errorf("skill.context_delivered_ref text = %q, want empty (the wrapper lives on skill.invoked)", ref)
	}
	if strings.Join(order, ",") != strings.Join(bareOrder, ",") {
		t.Errorf("event order changed: %v vs %v", order, bareOrder)
	}
	for _, ev := range events {
		if ev.Kind == session.KindSystem && ev.System.Subtype == "skill.invoked" && ev.Provenance.Line != 10 {
			t.Errorf("skill.invoked provenance line = %d, want 10", ev.Provenance.Line)
		}
	}

	// A contentId that does not hash the body sends the text out bare.
	mismatch := bytes.Replace(data, []byte("sha256:89f2a244"), []byte("sha256:00000000"), 1)
	if got, _, _, _ := skillTexts(t, mismatch, harness.TextDelivered); got != greeterBody {
		t.Errorf("hash mismatch: skill.invoked text = %q, want bare body", got)
	}

	// A skill.invoked as the last record is flushed bare at end of input.
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	truncated := bytes.Join(lines[:10], []byte("\n"))
	if got, _, _, _ := skillTexts(t, append(truncated, '\n'), harness.TextDelivered); got != greeterBody {
		t.Errorf("end of input: skill.invoked text = %q, want bare body", got)
	}
}
