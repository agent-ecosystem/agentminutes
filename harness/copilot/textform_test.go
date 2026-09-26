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

const (
	greeterBody = "# Greeter\n\nWhen asked to greet someone, use the create tool to write greeting.txt containing exactly: hello <name>. Then reply with exactly: greeted\n"
	// greeterWrapper is the content of the <skill-context> wrapper the
	// fixture's delivery records carry: the base directory and the
	// related-files list, which the bare form keeps.
	greeterWrapper = "Base directory for this skill: /tmp/exp/.github/skills/greeter\n\nRelated files (use view tool to read):\n  - /tmp/exp/.github/skills/greeter/references/greetings.md\n\n"
	greeterBare    = greeterWrapper + greeterBody
	greeterWrapped = "<skill-context name=\"greeter\">\n" + greeterBare + "\n</skill-context>"
)

// skillEvents parses data in the given form with line accounting and
// returns the system events by subtype (in order) plus the order of every
// system subtype.
func skillEvents(t *testing.T, data []byte, form harness.TextForm) (bySubtype map[string][]session.Event, order []string) {
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
	bySubtype = make(map[string][]session.Event)
	for _, ev := range s.Events {
		if ev.Kind != session.KindSystem {
			continue
		}
		order = append(order, ev.System.Subtype)
		bySubtype[ev.System.Subtype] = append(bySubtype[ev.System.Subtype], ev)
	}
	return bySubtype, order
}

func onlyText(t *testing.T, evs []session.Event, subtype string) string {
	t.Helper()
	if len(evs) != 1 {
		t.Fatalf("%d %s events, want 1", len(evs), subtype)
	}
	return evs[0].System.Text
}

// TestSkillTextForms pins both text forms on the skills fixture, whose
// skill is activated twice across a resume: skill.invoked carries the
// body, and the repeat activation is a skill.invoked_ref (content hash,
// no body) that resolves to the same body. In the bare form each
// activation's text is the wrapper's content lines (base directory,
// related files) plus the body, tags stripped; in the delivered form it
// is the body inside the recorded prefix and suffix verbatim. The
// delivery records stay textless, and event order and line accounting
// are unchanged by the one-record hold.
func TestSkillTextForms(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "skills.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var orders []string
	for form, want := range map[harness.TextForm]string{harness.TextBare: greeterBare, harness.TextDelivered: greeterWrapped} {
		by, order := skillEvents(t, data, form)
		orders = append(orders, strings.Join(order, ","))
		for _, subtype := range []string{"skill.invoked", "skill.invoked_ref"} {
			if got := onlyText(t, by[subtype], subtype); got != want {
				t.Errorf("%s: %s text = %q, want %q", form, subtype, got, want)
			}
		}
		if n := len(by["skill.context_delivered_ref"]); n != 2 {
			t.Fatalf("%s: %d delivery records, want 2", form, n)
		}
		for _, ev := range by["skill.context_delivered_ref"] {
			if ev.System.Text != "" {
				t.Errorf("%s: skill.context_delivered_ref text = %q, want empty (the wrapper rides on the activation event)", form, ev.System.Text)
			}
		}
		if line := by["skill.invoked"][0].Provenance.Line; line != 10 {
			t.Errorf("%s: skill.invoked provenance line = %d, want 10", form, line)
		}
		if line := by["skill.invoked_ref"][0].Provenance.Line; line != 37 {
			t.Errorf("%s: skill.invoked_ref provenance line = %d, want 37", form, line)
		}
		if !bytes.Contains(by["skill.invoked_ref"][0].System.Details, []byte(`"contentId":"sha256:89f2a244`)) {
			t.Errorf("%s: skill.invoked_ref details must keep the ref fields", form)
		}
	}
	if orders[0] != orders[1] {
		t.Errorf("event order differs between forms: %v vs %v", orders[0], orders[1])
	}

	// A contentId that does not hash the body sends the text out as the
	// body alone in both forms (the wrapper is not known to be its).
	mismatch := bytes.Replace(data, []byte("sha256:89f2a244"), []byte("sha256:00000000"), 1)
	for _, form := range []harness.TextForm{harness.TextBare, harness.TextDelivered} {
		by, _ := skillEvents(t, mismatch, form)
		if got := onlyText(t, by["skill.invoked"], "skill.invoked"); got != greeterBody {
			t.Errorf("%s: hash mismatch: skill.invoked text = %q, want bare body", form, got)
		}
	}

	// A skill.invoked as the last record is flushed as the body alone at
	// end of input.
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	truncated := append(bytes.Join(lines[:10], []byte("\n")), '\n')
	by, _ := skillEvents(t, truncated, harness.TextDelivered)
	if got := onlyText(t, by["skill.invoked"], "skill.invoked"); got != greeterBody {
		t.Errorf("end of input: skill.invoked text = %q, want bare body", got)
	}

	// A skill.invoked_ref whose hash no earlier skill.invoked in the
	// transcript carries (the body's record is gone) still counts as a
	// delivery: a textless event with the ref fields in details, in
	// place, with the delivery record after it.
	var unresolved [][]byte
	for i, l := range lines {
		if i != 9 && i != 10 { // drop the first activation and its delivery
			unresolved = append(unresolved, l)
		}
	}
	by, order := skillEvents(t, append(bytes.Join(unresolved, []byte("\n")), '\n'), harness.TextBare)
	if got := onlyText(t, by["skill.invoked_ref"], "skill.invoked_ref"); got != "" {
		t.Errorf("unresolved ref: text = %q, want empty", got)
	}
	if by["skill.invoked_ref"][0].Provenance.Line != 35 {
		t.Errorf("unresolved ref: provenance line = %d, want 35", by["skill.invoked_ref"][0].Provenance.Line)
	}
	if got := strings.Join(order, ","); !strings.Contains(got, "skill.invoked_ref,skill.context_delivered_ref") {
		t.Errorf("unresolved ref: order = %s", got)
	}
}

// TestSkillText pins the wrapper handling: the bare form strips only the
// <skill-context> tag lines, and a wrapper without them is kept whole.
func TestSkillText(t *testing.T) {
	cases := []struct {
		prefix, body, suffix string
		form                 harness.TextForm
		want                 string
	}{
		{"<skill-context name=\"x\">\nBase directory for this skill: /d\n\n", "body\n", "\n</skill-context>", harness.TextBare, "Base directory for this skill: /d\n\nbody\n"},
		{"<skill-context name=\"x\">\nBase directory for this skill: /d\n\n", "body\n", "\n</skill-context>", harness.TextDelivered, "<skill-context name=\"x\">\nBase directory for this skill: /d\n\nbody\n\n</skill-context>"},
		{"<skill-context name=\"x\">\n", "body\n", "\n</skill-context>", harness.TextBare, "body\n"},
		{"Loaded skill x:\n", "body\n", "\nEnd of skill.", harness.TextBare, "Loaded skill x:\nbody\n\nEnd of skill."},
	}
	for _, c := range cases {
		if got := skillText(c.prefix, c.body, c.suffix, c.form); got != c.want {
			t.Errorf("skillText(%q, %q, %q, %s) = %q, want %q", c.prefix, c.body, c.suffix, c.form, got, c.want)
		}
	}
}
