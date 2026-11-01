package claudecode

import (
	"strings"
	"testing"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/parseutil"
	"github.com/agent-ecosystem/agentminutes/session"
)

func systemBySubtype(s *session.Session) map[string][]*session.SystemEvent {
	m := map[string][]*session.SystemEvent{}
	for i := range s.Events {
		if ev := &s.Events[i]; ev.Kind == session.KindSystem {
			m[ev.System.Subtype] = append(m[ev.System.Subtype], ev.System)
		}
	}
	return m
}

// The requests fixture models the 2.1.309 shapes: the API request log
// (api-request-shape, api-request-blob per role, api-request), attachments
// with renderedRole, the prompt_snapshot's cliPrefix, an @-mentioned file
// attachment rendered as two messages, the Agent result's hand-back
// wrapper, and the artifact bookkeeping records on the skip list.
func TestRequestsFixture(t *testing.T) {
	s, data, skips := parseTestdata(t, "requests.jsonl")
	if un := parseutil.UncoveredLines(data, s.Events, skips); len(un) > 0 {
		t.Errorf("%d lines not covered (first: %d)", len(un), un[0])
	}
	// queue-operation (L1), artifact-comment-monitor (L22), artifact-autoreact-ledger (L23).
	if len(skips) != 3 || skips[0] != 1 || skips[1] != 22 || skips[2] != 23 {
		t.Errorf("skips = %v, want [1 22 23]", skips)
	}
	sys := systemBySubtype(s)

	// The shape's system blocks, blank-line-joined, are the static prompt
	// as sent, and equal the prompt_snapshot's cliPrefix plus sections.
	shape := sys["api-request-shape"]
	snap := sys["attachment/prompt_snapshot"]
	if len(shape) != 1 || len(snap) != 1 {
		t.Fatalf("shape %d snapshot %d", len(shape), len(snap))
	}
	wantPrompt := "You are a synthetic agent, built on a synthetic agent SDK.\n\nYou are a synthetic test assistant. Complete the task the user gives you with the tools available, and say when you are done.\n\nThe working directory is /tmp/exp and it is a git repository on branch main."
	if shape[0].Text != wantPrompt {
		t.Errorf("api-request-shape text = %q", shape[0].Text)
	}
	if snap[0].Text != wantPrompt {
		t.Errorf("prompt_snapshot text = %q, want the cliPrefix ahead of the sections", snap[0].Text)
	}

	// A user-role blob: the user-role reminders (wrapped) then the prompt;
	// bare text strips each block's wrapper.
	blobs := sys["api-request-blob/user"]
	if len(blobs) != 2 {
		t.Fatalf("user blobs = %d", len(blobs))
	}
	if !strings.HasPrefix(blobs[0].Text, "Codebase and user instructions are shown below.") ||
		strings.Contains(blobs[0].Text, "<system-reminder>") ||
		!strings.HasSuffix(blobs[0].Text, "\n\nReply with exactly: pong") {
		t.Errorf("user blob bare text = %q", blobs[0].Text)
	}
	// A system-role blob: the folded system block, no wrapper to strip.
	sb := sys["api-request-blob/system"]
	if len(sb) != 1 || !strings.HasPrefix(sb[0].Text, "# Environment\n") || !strings.HasSuffix(sb[0].Text, "<total_tokens>15000000 tokens left</total_tokens>") {
		t.Errorf("system blob = %+v", sb)
	}
	if ab := sys["api-request-blob/assistant"]; len(ab) != 1 || ab[0].Text != "pong" {
		t.Errorf("assistant blob = %+v", ab)
	}
	// api-request: no text, the record's own id.
	reqs := sys["api-request"]
	if len(reqs) != 2 || reqs[0].Text != "" {
		t.Errorf("api-request events = %+v", reqs)
	}
	var reqIDs []string
	for _, ev := range s.Events {
		if ev.Kind == session.KindSystem && ev.System.Subtype == "api-request" {
			reqIDs = append(reqIDs, ev.ID)
		}
	}
	if len(reqIDs) != 2 || reqIDs[0] != "req-1" || reqIDs[1] != "req-2" {
		t.Errorf("api-request ids = %v", reqIDs)
	}

	// An @-mentioned file: both rendered messages, stripped and joined.
	file := sys["attachment/file"]
	if len(file) != 1 || !strings.HasPrefix(file[0].Text, "Called the Read tool with the following input:") ||
		!strings.Contains(file[0].Text, "\n\nResult of calling the Read tool:\n1\t---\n") || strings.Contains(file[0].Text, "<system-reminder>") {
		t.Errorf("file attachment text = %q", file[0].Text)
	}

	// The Agent result's model-visible content is the hand-back wrapper.
	for _, ti := range s.ToolInteractions() {
		if ti.Call.ToolCall.Name == "Agent" {
			res := ti.Results[0].ToolResult
			if res.IsError || !strings.HasPrefix(res.Text(), "[Subagent hand-back]") || !strings.HasSuffix(res.Text(), "\n    drift-probe-subagent") {
				t.Errorf("Agent result = %q", res.Text())
			}
		}
	}
	st := s.Stats()
	if st.UserMessages != 2 || st.EventCounts[session.KindAssistantMessage] != 3 || st.ToolCalls != 1 {
		t.Errorf("stats = %+v", st)
	}
	if s.Totals == nil || s.Totals.InputTokens != 45 || s.Totals.OutputTokens != 47 || s.Totals.TotalPromptTokens != 6295 {
		t.Errorf("Totals = %+v", s.Totals)
	}
}

// In the delivered form the blobs' text blocks are verbatim, wrapper and
// all, blank-line-joined, as the request carried them.
func TestRequestsFixtureDeliveredForm(t *testing.T) {
	s := parseTestdataWith(t, "requests.jsonl", harness.Options{TextForm: harness.TextDelivered})
	sys := systemBySubtype(s)
	ub := sys["api-request-blob/user"][0].Text
	if !strings.HasPrefix(ub, "<system-reminder>\nCodebase and user instructions") || !strings.Contains(ub, "</system-reminder>\n\nReply with exactly: pong") {
		t.Errorf("delivered user blob = %q", ub)
	}
	if sb := sys["api-request-blob/system"][0].Text; strings.Contains(sb, "<system-reminder>") {
		t.Errorf("delivered system blob carries a wrapper the request did not: %q", sb)
	}
	if f := sys["attachment/file"][0].Text; !strings.HasPrefix(f, "<system-reminder>\nCalled the Read tool") || strings.Count(f, "</system-reminder>") != 2 {
		t.Errorf("delivered file attachment = %q", f)
	}
}
