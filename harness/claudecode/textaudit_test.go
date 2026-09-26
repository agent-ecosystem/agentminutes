package claudecode

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/textaudit"
	"github.com/agent-ecosystem/agentminutes/session"
)

// nonText enumerates the (subtype, path) pairs at which a system event's
// Details carry a long string that is not surfaced as Text, with the
// reason. textaudit.Invariant fails on any pair not listed here, so a
// harness release that adds a text-bearing record (or moves the text to a
// new key, as 2.1.27x did with `text`) is loud. Attachment details are
// the whole record, so paths are "attachment.<key>" for the attachment's
// own fields and bare for the envelope's. Attachments with no bare text
// field surface their rendered form, so an entry here means the string
// is not text the model saw as such.
var nonText = map[string]string{
	"attachment/* cwd": "a path (the record envelope)",
	"attachment/deferred_tools_record attachment.entries.[].*":          "tool definitions (descriptions, input schemas), a structured listing the model receives as tool schemas, not as text",
	"attachment/diagnostics attachment.files.[].diagnostics.[].message": "IDE diagnostics per file, structured; recorded on 2.1.231-236 with no rendered form",
	"attachment/edited_text_file attachment.filename":                   "a path (the snippet is the Text)",
	"attachment/instructions attachment.files.[].path":                  "paths (the bodies are the Text)",
	"attachment/queued_command attachment.prompt.[].source.data":        "pasted image bytes (base64)",
	"stop_hook_summary cwd":                                             "a path",
	"attachment/prompt_snapshot attachment.tools.[].*":                  "tool definitions, sent as the API's tools parameter, not as prompt text",
	"attachment/queued_command rendered.[].content":                     "the mid-turn rendering of a task notification; the adapter picks the rendering by position with the harness's own predicate (2.1.274 bundle), so the other one stays here",
	"attachment/queued_command renderedInHumanTurn.[].content":          "the human-turn rendering of a task notification; the adapter picks the rendering by position with the harness's own predicate (2.1.274 bundle), so the other one stays here",
	"attachment/environment attachment.snapshot.scratchpadDirectory":    "a path; the rendered text names it in its own phrasing",
	"tool_result/Agent prompt":                                          "the call's input (the delegated prompt); the result content is the subagent's report",
	"tool_result/Bash $":                                                "the error string with an Error: prefix; the result content carries it inside <tool_use_error> tags",
	"tool_result/Bash bashEditDiff.changedFiles.[]":                     "paths",
	"tool_result/Bash bashEditDiff.files.[].filePath":                   "a path",
	"tool_result/Bash bashEditDiff.files.[].hunks.[].lines.[]":          "the UI diff of files a command changed; the model saw the command output",
	"tool_result/Bash stdout":                                           "the persisted-output case: when output is too large the model sees a <persisted-output> notice with a preview, and the sidecar keeps the first 30 KB of stdout",
	"tool_result/Edit $":                                                "the error string with an Error: prefix; the result content carries it inside <tool_use_error> tags",
	"tool_result/Edit newString":                                        "the call's input",
	"tool_result/Edit oldString":                                        "the call's input",
	"tool_result/Edit originalFile":                                     "the pre-edit file, never shown; the result content is a confirmation",
	"tool_result/Edit structuredPatch.[].lines.[]":                      "the UI diff; the result content is a confirmation",
	"tool_result/NotebookEdit notebook_path":                            "a path",
	"tool_result/NotebookEdit original_file":                            "the pre-edit notebook, never shown",
	"tool_result/NotebookEdit updated_file":                             "the post-edit notebook; the result content is a confirmation",
	"tool_result/Read file.base64":                                      "image bytes; the model saw the image block",
	"tool_result/Read file.content":                                     "the raw file text; the model saw the numbered rendering in the result content",
	"tool_result/Read file.filePath":                                    "a path",
	"tool_result/Read file.outputDir":                                   "a path",
	"tool_result/TaskStop command":                                      "the stopped task's command; the result content is a confirmation",
	"tool_result/TaskStop message":                                      "the stopped task's last message, shown in the UI; the result content is a confirmation",
	"tool_result/WebFetch url":                                          "the fetched URL",
	"tool_result/WebSearch results.[].content.[].title":                 "the results appear in the content as a JSON-encoded Links list, so a title with characters JSON escapes is not a byte-for-byte substring",
	"tool_result/Write $":                                               "the error string with an Error: prefix; the result content carries it inside <tool_use_error> tags",
	"tool_result/Write content":                                         "the call's input (the written text); the result content is a confirmation",
	"tool_result/Write originalFile":                                    "the pre-write file, never shown",
	"tool_result/Write structuredPatch.[].lines.[]":                     "the UI diff; the result content is a confirmation",
}

// deliveryInvariant is the harness's own delivery marker as a check: an
// attachment record with a non-empty rendered form is text the harness
// injected, so its event must surface text, at any length. It has no
// threshold, so it catches the one-line reminders textaudit's length
// rule cannot.
func deliveryInvariant(t *testing.T, name string, events []session.Event) {
	t.Helper()
	for _, ev := range events {
		if ev.Kind != session.KindSystem || !strings.HasPrefix(ev.System.Subtype, "attachment/") || ev.System.Text != "" {
			continue
		}
		var rec struct {
			Rendered []struct {
				Content string `json:"content"`
			} `json:"rendered"`
		}
		if json.Unmarshal(ev.System.Details, &rec) != nil {
			continue
		}
		for _, r := range rec.Rendered {
			if r.Content != "" {
				t.Errorf("%s: %s (line %d) has a rendered form (%d bytes) but surfaces no text", name, ev.System.Subtype, ev.Provenance.Line, len(r.Content))
				break
			}
		}
	}
}

// TestFixturesTextAudit runs the text-surfacing invariants over every
// fixture, in both text forms: a system event or tool result whose
// details carry a long string either surfaces it (contains or is
// contained by its text) or is on the nonText list, and every rendered
// attachment surfaces text.
func TestFixturesTextAudit(t *testing.T) {
	names, err := filepath.Glob(filepath.Join("testdata", "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		for _, form := range []harness.TextForm{harness.TextBare, harness.TextDelivered} {
			s := parseTestdataWith(t, filepath.Base(name), harness.Options{TextForm: form})
			label := name + " (" + form.String() + ")"
			textaudit.Invariant(t, label, s.Events, nonText)
			deliveryInvariant(t, label, s.Events)
		}
	}
}
