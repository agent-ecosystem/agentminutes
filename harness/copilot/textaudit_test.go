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
	"external_tool.requested arguments.*":                                        "the call's input (an IDE-hosted tool's arguments), already on the tool_call event",
	"hook.start input.*":                                                         "the hook's input: the prompt (prompt, initialPrompt, transformedPrompt), the tool call arguments, or the tool result the hook fired for, each already on its own event (the transformed prompt being the user.message's unemitted transformedContent), plus transcriptPath, a path",
	"model.captured_assignment_context assignmentContext":                        "experiment assignment ids",
	"model.message message.*":                                                    "a helper-model call's response (session title generation, task labelling on a utility model), recorded in full; not the conversation, whose model text is on assistant.message",
	"model.message modelCall.api_id":                                             "an id",
	"model.messages_snapshot messages.[].*":                                      "a helper-model call's prompt and response (session title generation, task labelling on a utility model), recorded in full; not the conversation",
	"model.model_call_success modelCall.api_id":                                  "an id",
	"model.model_call_success requestMessages":                                   "a helper-model call's request, recorded in full; not the conversation",
	"model.model_call_success responseChunk.*":                                   "a helper-model call's response chunk, recorded in full; not the conversation",
	"model.response modelCall.api_id":                                            "an id",
	"model.response response.*":                                                  "a helper-model call's response, recorded in full; not the conversation",
	"permission.requested permissionRequest.args.*":                              "the call's input, already on the tool_call event",
	"permission.requested permissionRequest.commandSegments.[].fullCommandText":  "the command, the call's input",
	"permission.requested permissionRequest.commandSegments.[].staticTokens.[]":  "the command's tokens, the call's input",
	"permission.requested permissionRequest.commandSegments.[].workingDirectory": "a path",
	"permission.requested permissionRequest.fullCommandText":                     "the command, the call's input",
	"permission.requested permissionRequest.intention":                           "the approval prompt's intention line, shown to the user; UI, not model input",
	"permission.requested permissionRequest.newFileContents":                     "the file to be written, the call's input, shown in the approval prompt",
	"permission.requested permissionRequest.path":                                "a path",
	"permission.requested permissionRequest.possiblePaths.[]":                    "paths",
	"permission.requested permissionRequest.toolDescription":                     "the tool's description, a tool schema shown in the approval prompt",
	"permission.requested promptRequest.args.*":                                  "the call's input, already on the tool_call event",
	"permission.requested promptRequest.fullCommandText":                         "the command, the call's input",
	"permission.requested promptRequest.intention":                               "the approval prompt's intention line, shown to the user; UI, not model input",
	"permission.requested promptRequest.newFileContents":                         "the file to be written, the call's input, shown in the approval prompt",
	"permission.requested promptRequest.path":                                    "a path",
	"permission.requested promptRequest.paths.[]":                                "paths",
	"permission.requested promptRequest.toolDescription":                         "the tool's description, a tool schema shown in the approval prompt",
	"permission.requested permissionRequest.diff":                                "the approval prompt's diff, shown to the user; UI, not model input",
	"permission.requested permissionRequest.fileName":                            "a path",
	"permission.requested promptRequest.diff":                                    "the approval prompt's diff, shown to the user; UI, not model input",
	"permission.requested promptRequest.fileName":                                "a path",
	"session.binary_asset data":                                                  "image bytes (base64); the referencing tool result carries the image block",
	"session.binary_asset description":                                           "the image descriptor, which also rides on the referencing tool result's image block",
	"session.compaction_complete checkpointPath":                                 "a path",
	"session.resume context.cwd":                                                 "a path",
	"session.shutdown accountingSnapshot.codeChanges.filesModified.[]":           "paths",
	"session.shutdown codeChanges.filesModified.[]":                              "paths",
	"session.usage_checkpoint accountingSnapshot.codeChanges.filesModified.[]":   "paths",
	"session.usage_checkpoint promptCacheBreakState.[].models.*.model_call_id":   "ids (one key per model name)",
	"session.usage_record usage.accounting.usageId":                              "an id",
	"session.usage_record usage.apiCallId":                                       "an id",
	"skill.context_delivered_ref prefix":                                         "the delivery wrapper around a skill body; its content lines (base directory, related files) are in the preceding skill.invoked or skill.invoked_ref event's Text in both forms, its tag line in the delivered form",
	"skill.invoked description":                                                  "the skill's listing description, which the system.message's <available_skills> block surfaces as that event's Text",
	"skill.invoked path":                                                         "a path",
	"skill.invoked_ref description":                                              "the skill's listing description, which the system.message's <available_skills> block surfaces as that event's Text",
	"skill.invoked_ref path":                                                     "a path",
	"tool_result/* result.binaryResultsForLlm.[].description":                    "the image descriptor, carried on the result's image block",
	"tool_result/* result.detailedContent":                                       "the UI's detailed rendering (a diff, a numbered file, an agent's transcript); the model receives result.content",
	"tool_result/* toolTelemetry.restrictedProperties.filePaths":                 "paths",
	"tool_result/* toolTelemetry.restrictedProperties.large_output_file":         "a path (where an oversized output was spooled)",
	"tool_result/* toolTelemetry.restrictedProperties.pattern":                   "the call's input (a search pattern)",
	"tool_result/* toolTelemetry.restrictedProperties.query":                     "the call's input (a query)",
	"tool.execution_start arguments":                                             "the call's input, when the record carries it as one string, already on the tool_call event",
	"tool.execution_start arguments.*":                                           "the call's input, whatever its keys, already on the tool_call event",
	"tool.execution_start shellToolInfo.possiblePaths.[]":                        "paths",
}

// deliveryInvariant is the harness's own delivery marker as a check: a
// skill.context_delivered_ref names, by SHA-256, the content it wrapped
// and delivered, so the activation event just before it (skill.invoked,
// or skill.invoked_ref resolved to the earlier body) must surface text
// that is that content inside the record's wrapper in the parsed form:
// the wrapper's content lines around the body in the bare form, the
// prefix and suffix verbatim in the delivered form. It has no threshold,
// and it holds every delivery to its own activation event, so a repeat
// delivery cannot pass on the strength of the first.
func deliveryInvariant(t *testing.T, name string, events []session.Event, form harness.TextForm) {
	t.Helper()
	var last *session.Event
	for i := range events {
		ev := &events[i]
		if ev.Kind != session.KindSystem {
			continue
		}
		switch ev.System.Subtype {
		case "skill.invoked", "skill.invoked_ref":
			last = ev
		case "skill.context_delivered_ref":
			var ref struct {
				ContentID string `json:"contentId"`
				Prefix    string `json:"prefix"`
				Suffix    string `json:"suffix"`
			}
			if json.Unmarshal(ev.System.Details, &ref) != nil || ref.ContentID == "" {
				continue
			}
			if last == nil {
				t.Errorf("%s: skill.context_delivered_ref (line %d) has no activation event before it", name, ev.Provenance.Line)
				continue
			}
			// The wrapper in this form, split where the body goes.
			prefix, suffix, _ := strings.Cut(skillText(ref.Prefix, "\x00", ref.Suffix, form), "\x00")
			text := last.System.Text
			body, okp := strings.CutPrefix(text, prefix)
			body, oks := strings.CutSuffix(body, suffix)
			if !okp || !oks || contentHash(body) != ref.ContentID {
				t.Errorf("%s: skill.context_delivered_ref (line %d) names content %s, but the %s event before it (line %d) does not surface that content in its wrapper (%s form): text = %q", name, ev.Provenance.Line, ref.ContentID, last.System.Subtype, last.Provenance.Line, form, text)
			}
			last = nil
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
			deliveryInvariant(t, label, s.Events, form)
		}
	}
}
