package claudecode

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/session"
)

// TestDeliveredTextForm pins Options.TextForm = TextDelivered on the
// attachments fixture: attachments with a rendered form carry it verbatim
// (wrapper included), and those without keep their bare text.
func TestDeliveredTextForm(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "attachments.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := harness.Parse(Adapter{}, bytes.NewReader(data), harness.Options{TextForm: harness.TextDelivered})
	if err != nil {
		t.Fatal(err)
	}
	texts := map[string]string{}
	for _, ev := range s.Events {
		if ev.Kind == session.KindSystem {
			texts[ev.System.Subtype] = ev.System.Text
		}
	}
	// Rendered form, wrapper included, for a type whose bare form is a field.
	if got := texts["attachment/model"]; !strings.HasPrefix(got, "<system-reminder>\nYou are powered by the model named Synthetic 1.") || !strings.HasSuffix(got, "\n</system-reminder>") {
		t.Errorf("model delivered text = %q, want the rendered block verbatim", got)
	}
	// Rendered form for instructions carries the per-file framing bare drops.
	if got := texts["attachment/instructions"]; !strings.Contains(got, "Contents of /tmp/exp/CLAUDE.md:") || !strings.HasPrefix(got, "<system-reminder>") {
		t.Errorf("instructions delivered text = %q, want the rendered block with file headers", got)
	}
	// No rendered form: bare text is kept.
	if got := texts["attachment/prompt_snapshot"]; !strings.HasPrefix(got, "You are a synthetic test assistant.") {
		t.Errorf("prompt_snapshot delivered text = %q, want the bare join (never injected)", got)
	}
	if got := texts["attachment/batching_reminder_sent"]; !strings.HasPrefix(got, "First privately list") {
		t.Errorf("batching_reminder_sent (no rendered) delivered text = %q, want bare", got)
	}
	// Rendered-only type: the wrapper is kept in delivered form.
	if got := texts["attachment/environment"]; !strings.HasPrefix(got, "<system-reminder>\n# Environment") {
		t.Errorf("environment delivered text = %q, want the rendered block verbatim", got)
	}
}
