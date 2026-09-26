package copilot

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/textaudit"
	"github.com/agent-ecosystem/agentminutes/session"
)

// nonText enumerates the (subtype, path) pairs at which a system event's
// Details carry a long string that is not surfaced as Text, with the
// reason; see textaudit.
var nonText = map[string]string{
	"permission.requested permissionRequest.commandSegments.[].workingDirectory": "a path",
	"permission.requested permissionRequest.diff":                                "the approval prompt's diff, shown to the user; UI, not model input",
	"permission.requested permissionRequest.fileName":                            "a path",
	"permission.requested promptRequest.diff":                                    "the approval prompt's diff, shown to the user; UI, not model input",
	"permission.requested promptRequest.fileName":                                "a path",
	"session.binary_asset data":                                                  "image bytes (base64); the referencing tool result carries the image block",
	"session.binary_asset description":                                           "the image descriptor, which also rides on the referencing tool result's image block",
	"session.resume context.cwd":                                                 "a path",
	"session.shutdown codeChanges.filesModified.[]":                              "paths",
	"session.usage_checkpoint promptCacheBreakState.[].models.*.model_call_id":   "ids (one key per model name)",
	"skill.context_delivered_ref prefix":                                         "the delivery wrapper around the skill.invoked body, which is that event's Text",
	"skill.invoked path":                                                         "a path",
	"tool_result/* result.binaryResultsForLlm.[].description":                    "the image descriptor, carried on the result's image block",
	"tool_result/* result.detailedContent":                                       "the UI's detailed rendering (a diff, a numbered file, an agent's transcript); the model receives result.content",
	"tool_result/* toolTelemetry.restrictedProperties.filePaths":                 "paths",
	"tool.execution_start arguments.*":                                           "the call's input, whatever its keys, already on the tool_call event",
	"tool.execution_start shellToolInfo.possiblePaths.[]":                        "paths",
}

// deliveryInvariant is the harness's own delivery marker as a check: a
// skill.context_delivered_ref names, by SHA-256, the content it wrapped
// and delivered, so some earlier skill.invoked event must surface text
// that hashes to it (bare, or wrapped in that record's prefix and suffix
// in the delivered form). It has no threshold and does not depend on
// which record carries the body.
func deliveryInvariant(t *testing.T, name string, events []session.Event) {
	t.Helper()
	var bodies []string
	for _, ev := range events {
		if ev.Kind != session.KindSystem {
			continue
		}
		switch ev.System.Subtype {
		case "skill.invoked":
			bodies = append(bodies, ev.System.Text)
		case "skill.context_delivered_ref":
			var ref struct {
				ContentID string `json:"contentId"`
				Prefix    string `json:"prefix"`
				Suffix    string `json:"suffix"`
			}
			if json.Unmarshal(ev.System.Details, &ref) != nil || ref.ContentID == "" {
				continue
			}
			found := false
			for _, b := range bodies {
				bare := strings.TrimSuffix(strings.TrimPrefix(b, ref.Prefix), ref.Suffix)
				if contentHash(bare) == ref.ContentID {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s: skill.context_delivered_ref (line %d) names content %s that no skill.invoked event's text hashes to", name, ev.Provenance.Line, ref.ContentID)
			}
		}
	}
}

// TestFixturesTextAudit runs the text-surfacing invariants over every
// fixture, in both text forms.
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
		for _, form := range []harness.TextForm{harness.TextBare, harness.TextDelivered} {
			s, err := harness.Parse(Adapter{}, bytes.NewReader(data), harness.Options{TextForm: form})
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			label := name + " (" + form.String() + ")"
			textaudit.Invariant(t, label, s.Events, nonText)
			deliveryInvariant(t, label, s.Events)
		}
	}
}
