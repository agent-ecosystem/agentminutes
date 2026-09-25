package agentminutes_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/agent-ecosystem/agentminutes"
	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/session"
)

const userLine = `{"parentUuid":null,"isSidechain":false,"type":"user","message":{"role":"user","content":"hello"},"uuid":"u-1","timestamp":"2026-07-19T10:00:00.000Z","userType":"external","entrypoint":"cli","cwd":"/tmp","sessionId":"s-1","version":"2.1.197","gitBranch":"main"}` + "\n"

func TestAdapterFor(t *testing.T) {
	a, err := agentminutes.AdapterFor(harness.ClaudeCode)
	if err != nil || a.ID() != harness.ClaudeCode {
		t.Errorf("AdapterFor(claude-code) = %v, %v", a, err)
	}
	if _, err := agentminutes.AdapterFor("not-a-harness"); err == nil {
		t.Error("AdapterFor(unknown): want error")
	}
}

// TestLastValidatedCoversRegistry keeps harness.LastValidated in step with
// the adapter registry: a new adapter must record the release its format
// inventory was validated against.
func TestLastValidatedCoversRegistry(t *testing.T) {
	for _, a := range agentminutes.Adapters() {
		if harness.LastValidated(a.ID()) == "" {
			t.Errorf("harness %q has no LastValidated entry", a.ID())
		}
	}
}

func TestLocatorFor(t *testing.T) {
	l, err := agentminutes.LocatorFor(harness.ClaudeCode)
	if err != nil || l.ID() != harness.ClaudeCode {
		t.Errorf("LocatorFor(claude-code) = %v, %v", l, err)
	}
	if _, err := agentminutes.LocatorFor("not-a-harness"); err == nil {
		t.Error("LocatorFor(unknown): want error")
	}
}

// TestLocatorsCoverAdapters keeps the two registries in step: every parseable
// harness must also be discoverable, and both lists stay alphabetical.
func TestLocatorsCoverAdapters(t *testing.T) {
	locators := agentminutes.Locators()
	adapters := agentminutes.Adapters()
	if len(locators) != len(adapters) {
		t.Fatalf("%d locators, %d adapters", len(locators), len(adapters))
	}
	for i, a := range adapters {
		if locators[i].ID() != a.ID() {
			t.Errorf("locator %d = %q, adapter %d = %q", i, locators[i].ID(), i, a.ID())
		}
		if root, err := locators[i].DefaultRoot(); err != nil || root == "" {
			t.Errorf("%s: DefaultRoot() = %q, %v", a.ID(), root, err)
		}
	}
}

func TestDetect(t *testing.T) {
	a, conf := agentminutes.Detect([]byte(userLine))
	if conf != harness.Certain || a == nil || a.ID() != harness.ClaudeCode {
		t.Errorf("Detect(claude line) = %v, %v", a, conf)
	}
	codexLine := `{"timestamp":"2026-07-19T22:00:00.000Z","type":"session_meta","payload":{"session_id":"s-1","cli_version":"0.144.1","cwd":"/tmp"}}`
	a, conf = agentminutes.Detect([]byte(codexLine))
	if conf != harness.Certain || a == nil || a.ID() != harness.Codex {
		t.Errorf("Detect(codex line) = %v, %v", a, conf)
	}

	agyLine := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-07-19T23:00:00Z","content":"<USER_REQUEST>hi</USER_REQUEST>"}`
	a, conf = agentminutes.Detect([]byte(agyLine))
	if conf != harness.Certain || a == nil || a.ID() != harness.Antigravity {
		t.Errorf("Detect(antigravity line) = %v, %v", a, conf)
	}

	copilotLine := `{"type":"session.start","data":{"sessionId":"s-1","version":1,"producer":"copilot-agent","copilotVersion":"1.0.88","context":{"cwd":"/tmp"}},"id":"e-1","timestamp":"2026-09-25T15:00:00.000Z","parentId":null}`
	a, conf = agentminutes.Detect([]byte(copilotLine))
	if conf != harness.Certain || a == nil || a.ID() != harness.Copilot {
		t.Errorf("Detect(copilot line) = %v, %v", a, conf)
	}

	a, conf = agentminutes.Detect([]byte("not a transcript"))
	if conf != harness.NoMatch || a != nil {
		t.Errorf("Detect(garbage) = %v, %v", a, conf)
	}
}

func TestParse(t *testing.T) {
	s, err := agentminutes.Parse(strings.NewReader(userLine), harness.ClaudeCode, harness.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Meta.SessionID != "s-1" || len(s.Events) != 2 {
		t.Errorf("session = meta %+v, %d events", s.Meta, len(s.Events))
	}
	if s.Events[1].Kind != session.KindUserMessage || s.Events[1].UserMessage.Text() != "hello" {
		t.Errorf("event 1 = %+v", s.Events[1])
	}

	if _, err := agentminutes.Parse(strings.NewReader(userLine), "not-a-harness", harness.Options{}); err == nil {
		t.Error("Parse(unknown harness): want error")
	}
}

// setHome points every locator's DefaultRoot at dir for the test's duration
// (os.UserHomeDir reads HOME on Unix, USERPROFILE on Windows).
func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

// TestScanSkipsMissingRoots pins Scan's behavior on a machine with no
// harness installed: every registered root is reported via OnSkip with
// SkipReasonRootMissing and nothing is yielded.
func TestScanSkipsMissingRoots(t *testing.T) {
	setHome(t, t.TempDir())
	var skips []string
	opts := harness.ScanOptions{OnSkip: func(_, reason string) {
		skips = append(skips, reason)
	}}
	for ref, err := range agentminutes.Scan(opts) {
		t.Errorf("unexpected yield: %+v, %v", ref, err)
	}
	if len(skips) != len(agentminutes.Locators()) {
		t.Fatalf("%d skips, want %d", len(skips), len(agentminutes.Locators()))
	}
	for _, reason := range skips {
		if reason != agentminutes.SkipReasonRootMissing {
			t.Errorf("skip reason = %q, want %q", reason, agentminutes.SkipReasonRootMissing)
		}
	}
}

// TestScanYieldsScanErrors pins the facade contract that every error Scan
// yields is a *harness.ScanError, including for unreadable transcripts.
func TestScanYieldsScanErrors(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	proj := filepath.Join(home, ".claude", "projects", "-tmp-proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "bad.jsonl"), []byte("not a transcript\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var errs []error
	for _, err := range agentminutes.Scan(harness.ScanOptions{}) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
	}
	var scanErr *harness.ScanError
	if !errors.As(errs[0], &scanErr) {
		t.Fatalf("error is %T, want *harness.ScanError: %v", errs[0], errs[0])
	}
	if scanErr.Harness != harness.ClaudeCode || scanErr.Path == "" {
		t.Errorf("ScanError = %+v, want claude-code with path", scanErr)
	}
}

// TestScanHomelessYieldsScanErrors pins that even a DefaultRoot failure
// (no resolvable home directory) surfaces as *harness.ScanError, so
// consumers matching on the type see every failure path.
func TestScanHomelessYieldsScanErrors(t *testing.T) {
	setHome(t, "")
	var errs []error
	for _, err := range agentminutes.Scan(harness.ScanOptions{}) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) != len(agentminutes.Locators()) {
		t.Fatalf("got %d errors, want %d: %v", len(errs), len(agentminutes.Locators()), errs)
	}
	for _, err := range errs {
		var scanErr *harness.ScanError
		if !errors.As(err, &scanErr) {
			t.Errorf("error is %T, want *harness.ScanError: %v", err, err)
		}
	}
}

// TestTask pins task-scope accounting on a synthetic Claude Code layout:
// the parent and its two subagent transcripts are summarized separately
// and as one aggregate whose totals are the exact sum (no transcript
// counted twice, the parent's own numbers unchanged), with a per-agent
// split keyed by subagent id.
func TestTask(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "-tmp-exp")
	rec := func(sessionID, uuid, parent, ts string, sidechain bool, agentID, kind, body string) string {
		side := "false"
		if sidechain {
			side = "true"
		}
		agent := ""
		if agentID != "" {
			agent = `"agentId":"` + agentID + `",`
		}
		return `{"parentUuid":` + parent + `,"isSidechain":` + side + `,` + agent + `"type":"` + kind + `","message":` + body + `,"uuid":"` + uuid + `","timestamp":"` + ts + `","userType":"external","entrypoint":"cli","cwd":"/tmp/exp","sessionId":"` + sessionID + `","version":"2.1.274","gitBranch":"main"}` + "\n"
	}
	assistant := func(model string, in, out int, text string) string {
		return `{"id":"m-` + text + `","model":"` + model + `","role":"assistant","type":"message","stop_reason":"end_turn","content":[{"type":"text","text":"` + text + `"}],"usage":{"input_tokens":` + itoa(in) + `,"output_tokens":` + itoa(out) + `,"cache_read_input_tokens":100,"cache_creation_input_tokens":10}}`
	}
	parent := rec("p-1", "u-1", "null", "2026-09-25T10:00:00.000Z", false, "", "user", `{"role":"user","content":"delegate"}`) +
		rec("p-1", "a-1", `"u-1"`, "2026-09-25T10:00:05.000Z", false, "", "assistant", assistant("m-parent", 4, 20, "parent-done"))
	child := func(id, text string, in, out int) string {
		return rec("p-1", "u-"+id, "null", "2026-09-25T10:00:01.000Z", true, id, "user", `{"role":"user","content":"child task"}`) +
			rec("p-1", "a-"+id, `"u-`+id+`"`, "2026-09-25T10:00:03.000Z", true, id, "assistant", assistant("m-child", in, out, text))
	}
	mustWrite(t, filepath.Join(proj, "p-1.jsonl"), parent)
	mustWrite(t, filepath.Join(proj, "p-1", "subagents", "agent-c1.jsonl"), child("c1", "child-one", 7, 30))
	mustWrite(t, filepath.Join(proj, "p-1", "subagents", "agent-c2.jsonl"), child("c2", "child-two", 9, 40))

	ts, err := agentminutes.Task(harness.ClaudeCode, root, filepath.Join(proj, "p-1.jsonl"), harness.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if ts.Harness != "claude-code" || ts.SessionID != "p-1" || ts.Join != harness.JoinLayout || len(ts.Transcripts) != 3 {
		t.Fatalf("task = harness %q session %q join %q transcripts %d", ts.Harness, ts.SessionID, ts.Join, len(ts.Transcripts))
	}
	p := ts.Transcripts[0]
	if p.IsSubagent || p.Stats.Totals.InputTokens != 4 || p.Stats.FinalAnswer != "parent-done" {
		t.Errorf("parent transcript = %+v", p)
	}
	if ts.Transcripts[1].SubagentID != "c1" || !ts.Transcripts[1].IsSubagent || ts.Transcripts[2].SubagentID != "c2" {
		t.Errorf("subagent transcripts = %+v", ts.Transcripts[1:])
	}
	// Aggregate: exact sums, Anthropic convention re-derived, parent's answer.
	tot := ts.Task.Totals
	if tot == nil || tot.InputTokens != 4+7+9 || tot.OutputTokens != 20+30+40 || tot.CacheReadInputTokens != 300 || tot.TotalPromptTokens != 20+300+30 {
		t.Errorf("task totals = %+v", tot)
	}
	if ts.Task.FinalAnswer != "parent-done" || ts.Task.Events != 9 || ts.Task.EventCounts[session.KindAssistantMessage] != 3 {
		t.Errorf("task stats = final %q events %d", ts.Task.FinalAnswer, ts.Task.Events)
	}
	if got := ts.Task.Models; len(got) != 2 || got[0] != "m-parent" || got[1] != "m-child" {
		t.Errorf("task models = %v", got)
	}
	if len(ts.Task.ByAgent) != 3 || ts.Task.ByAgent[""].Totals.InputTokens != 4 || ts.Task.ByAgent["c1"].Totals.OutputTokens != 30 || ts.Task.ByAgent["c2"].Totals.TotalPromptTokens != 9+100+10 {
		t.Errorf("by_agent = %+v", ts.Task.ByAgent)
	}
	// The sum property holds against the per-transcript summaries.
	var in int64
	for _, tr := range ts.Transcripts {
		in += tr.Stats.Totals.InputTokens
	}
	if in != tot.InputTokens {
		t.Errorf("sum of parts %d != task %d", in, tot.InputTokens)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestTaskStatsFieldProvenance keeps the task summary under the same
// schema-tag discipline as the session types: every exported field of the
// facade's output structs declares its provenance.
func TestTaskStatsFieldProvenance(t *testing.T) {
	for _, root := range []any{agentminutes.TaskStats{}, agentminutes.TranscriptStats{}} {
		rt := reflect.TypeOf(root)
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			switch f.Tag.Get("schema") {
			case "acp", "otel", "ext":
			default:
				t.Errorf("%s.%s has no schema tag", rt.Name(), f.Name)
			}
		}
	}
}
