package claudecode

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

func parseTestdata(t *testing.T, name string) (*session.Session, []byte, []int) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var skips []int
	s, err := harness.Parse(Adapter{}, bytes.NewReader(data), harness.Options{
		OnSkip: func(line int, _ string) { skips = append(skips, line) },
	})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return s, data, skips
}

func parseTestdataWith(t *testing.T, name string, opts harness.Options) *session.Session {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	s, err := harness.Parse(Adapter{}, bytes.NewReader(data), opts)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return s
}

// The attachments fixture models the 2.1.274 attachment shapes: a
// prompt_snapshot (systemPrompt sections around the boundary sentinel),
// the reminders that carry their text under `text` rather than
// `content`, a listing under `content`, the type-specific fields, and
// the structured attachments whose only text is the rendered form
// (environment, date, bash_output_audience_note). A record with a
// rendered form surfaces it with the tags stripped; one without (as
// before 2.1.267) surfaces its field; prompt_render_point carries none.
func TestAttachmentsFixtureText(t *testing.T) {
	s, data, skips := parseTestdata(t, "attachments.jsonl")
	if un := parseutil.UncoveredLines(data, s.Events, skips); len(un) > 0 {
		t.Errorf("%d lines not covered (first: %d)", len(un), un[0])
	}
	want := map[string]string{
		// No rendered form: the field paths.
		"attachment/prompt_snapshot":        "You are a synthetic test assistant. Complete the task the user gives you with the tools available, and say when you are done.\n\nToday's synthetic date is 2026-09-25. The working directory is /tmp/exp and it is a git repository on branch main.",
		"attachment/batching_reminder_sent": "First privately list what you need next; then request every item that doesn't depend on another's result in this one response.",
		"attachment/agent_listing_delta":    "- runner: Runs a synthetic task end to end and reports the result in one line (tools: bash, read).\n- checker: Reads a file and reports whether it matches the expected synthetic content exactly.",
		"attachment/edited_text_file":       "1\t# exp\n2\t\n3\tThis synthetic project has one rule: greet by writing greeting.txt, then reply with the single word greeted.",
		"attachment/prompt_render_point":    "",
		// Rendered form, tags stripped.
		"attachment/skill_listing":             "The following skills are available for use:\n- greeter: Produces a greeting file. Use when asked to greet someone by name.\n- probe: A synthetic skill that exists only so the listing has two entries.",
		"attachment/model":                     "You are powered by the model named Synthetic 1. The exact model ID is synthetic-1. Assistant knowledge cutoff is June 2026.",
		"attachment/total_tokens_reminder":     "<total_tokens>15000000 tokens left</total_tokens>",
		"attachment/silent_turn_reminder":      "The user hasn't heard from you in a while: say in a few words what you're doing, then continue.",
		"attachment/date":                      "Today's date is 2026-09-25.",
		"attachment/instructions":              "Codebase and user instructions are shown below.\n\nContents of /tmp/home/.claude/CLAUDE.md:\n\n# Global Instructions\n\nAlways answer in one short sentence and never invent file names that do not exist in the workspace.\n\nContents of /tmp/exp/CLAUDE.md:\n\n# exp\n\nThis synthetic project has one rule: greet by writing greeting.txt, then reply with the single word greeted.",
		"attachment/deferred_tools_delta":      "The following tools just became available and are ready to use:\nMonitor\nNotebookEdit",
		"attachment/mcp_instructions_delta":    "# MCP Server Instructions\n\n## synthetic-docs\nSynthetic docs server: call guide first, then read; never write unless the user asked for a document.",
		"attachment/read_truncation_notice":    "[Truncated: PARTIAL view of /tmp/exp/big.log, lines 1-2000 of 9000. Re-read with offset to see the rest of the file.]",
		"attachment/environment":               "# Environment\nYou have been invoked in the following environment: \n - Primary working directory: /tmp/exp\n - Is a git repository: true\n - Platform: darwin",
		"attachment/bash_output_audience_note": "Only you see that command's output: the user's terminal shows at most a few lines of it. If the user needs to read any of it, put it in your reply.",
	}
	// queued_command appears four times (typed prompt, prompt with a
	// pasted image, a task notification in the human's turn, and one
	// mid-turn after the assistant reply); checked by order below. The
	// two notifications carry both renderings; position picks.
	notification := "<task-notification>\n<task-id>t-synthetic-1</task-id>\n<status>completed</status>\n<summary>The background check finished: greeting.txt contains hello World.</summary>\n</task-notification>"
	wantQueued := []string{
		"Also, after greeting, tell me how many files are in the working directory now; I queued this while you were working.",
		"[Image #1] Compare this screenshot with the greeting file and say whether they agree.",
		"[SYSTEM NOTIFICATION - NOT USER INPUT]\nThis is an automated background-task event, NOT a message from the user. It is delivered in the same turn as the user's message; the user's message is real input.\n" + notification,
		"[SYSTEM NOTIFICATION - NOT USER INPUT]\nThis is an automated background-task event, NOT a message from the user.\n" + notification,
	}
	var gotQueued []string
	seen := map[string]bool{}
	for _, ev := range s.Events {
		if ev.Kind != session.KindSystem {
			continue
		}
		if ev.System.Subtype == "attachment/queued_command" {
			gotQueued = append(gotQueued, ev.System.Text)
			if !bytes.Contains(ev.System.Details, []byte(`"rendered"`)) {
				t.Errorf("queued_command details should be the whole record (envelope rendered key missing)")
			}
			continue
		}
		w, ok := want[ev.System.Subtype]
		if !ok {
			t.Errorf("unexpected system subtype %q", ev.System.Subtype)
			continue
		}
		seen[ev.System.Subtype] = true
		if ev.System.Text != w {
			t.Errorf("%s text = %q, want %q", ev.System.Subtype, ev.System.Text, w)
		}
		if strings.Contains(ev.System.Text, promptBoundary) {
			t.Errorf("%s text carries the boundary sentinel", ev.System.Subtype)
		}
	}
	for subtype := range want {
		if !seen[subtype] {
			t.Errorf("fixture emitted no %s event", subtype)
		}
	}
	if len(gotQueued) != len(wantQueued) {
		t.Fatalf("queued_command texts = %q, want %q", gotQueued, wantQueued)
	}
	for i := range wantQueued {
		if gotQueued[i] != wantQueued[i] {
			t.Errorf("queued_command[%d] text = %q, want %q", i, gotQueued[i], wantQueued[i])
		}
	}
}
