package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-ecosystem/agentminutes/session"
)

var fixture = filepath.Join("..", "..", "harness", "claudecode", "testdata", "session.jsonl")

func run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := newRootCmd()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err = root.Execute()
	return out.String(), errOut.String(), err
}

func TestConvertJSON(t *testing.T) {
	stdout, _, err := run(t, "convert", fixture)
	if err != nil {
		t.Fatal(err)
	}
	var s session.Session
	if err := json.Unmarshal([]byte(stdout), &s); err != nil {
		t.Fatalf("output is not a session record: %v", err)
	}
	if s.SchemaVersion != session.SchemaVersion {
		t.Errorf("schema version = %q, want %q", s.SchemaVersion, session.SchemaVersion)
	}
	if len(s.Events) != 13 || s.Meta.SessionID != "s-fixture" {
		t.Errorf("got %d events, meta %+v", len(s.Events), s.Meta)
	}
	if s.Report.SkippedRecords["mode"] != 1 {
		t.Errorf("report = %+v, want skip accounting", s.Report)
	}
}

func TestConvertJSONL(t *testing.T) {
	stdout, stderr, err := run(t, "convert", "--format", "jsonl", fixture)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 13 {
		t.Fatalf("got %d jsonl lines, want 13", len(lines))
	}
	for i, ln := range lines {
		var ev session.Event
		if err := json.Unmarshal([]byte(ln), &ev); err != nil {
			t.Fatalf("line %d: %v", i+1, err)
		}
		if err := ev.Validate(); err != nil {
			t.Errorf("line %d: %v", i+1, err)
		}
	}
	// The stderr summary must carry the same per-type breakdown as the json
	// report's skipped_records, so jsonl consumers can audit what dropped.
	if !strings.Contains(stderr, "13 events, 4 skipped records (ai-title 1, file-history-delta 1, file-history-snapshot 1, mode 1)") {
		t.Errorf("stderr = %q, want per-type skip summary", stderr)
	}
}

func TestConvertExplicitHarness(t *testing.T) {
	if _, _, err := run(t, "convert", "--harness", "claude-code", fixture); err != nil {
		t.Errorf("explicit harness: %v", err)
	}
	if _, _, err := run(t, "convert", "--harness", "not-a-harness", fixture); err == nil {
		t.Error("unknown harness: want error")
	}
}

func TestConvertHarnessVersionFlag(t *testing.T) {
	// Antigravity transcripts record no version; the flag supplies it as
	// metadata.
	agFixture := filepath.Join("..", "..", "harness", "antigravity", "testdata", "transcript_full.jsonl")
	stdout, _, err := run(t, "convert", "--harness-version", "1.1.1", agFixture)
	if err != nil {
		t.Fatal(err)
	}
	var s session.Session
	if err := json.Unmarshal([]byte(stdout), &s); err != nil {
		t.Fatalf("output is not a session record: %v", err)
	}
	if s.Meta.HarnessVersion != "1.1.1" {
		t.Errorf("harness_version = %q, want the flag value", s.Meta.HarnessVersion)
	}
}

func TestStatsCommand(t *testing.T) {
	stdout, _, err := run(t, "stats", fixture)
	if err != nil {
		t.Fatal(err)
	}
	var st session.Stats
	if err := json.Unmarshal([]byte(stdout), &st); err != nil {
		t.Fatalf("output is not a stats record: %v", err)
	}
	// The claudecode fixture has three tool calls (Read, WebFetch, orphan
	// aside) and a WebFetch with raw byte accounting.
	if st.ToolCalls != 2 || st.ToolCallsByName["Read"] != 1 || st.ToolCallsByName["WebFetch"] != 1 {
		t.Errorf("stats = %+v", st.ToolCallsByName)
	}
	if st.FetchRawBytes != 52000 {
		t.Errorf("FetchRawBytes = %d, want 52000", st.FetchRawBytes)
	}
	if st.FinalAnswer == "" || st.Totals == nil || st.Totals.OutputTokens == 0 {
		t.Errorf("final answer %q / totals %+v", st.FinalAnswer, st.Totals)
	}
}

func TestConvertPromote(t *testing.T) {
	codexInput := `{"timestamp":"2026-07-19T22:00:00.000Z","type":"session_meta","payload":{"session_id":"s","cli_version":"0.144.1","cwd":"/tmp"}}` + "\n" +
		`{"timestamp":"2026-07-19T22:00:02.000Z","type":"event_msg","payload":{"type":"web_search_end","call_id":"exec-abc","query":"https://example.com/","action":{"type":"open_page","url":"https://example.com/"}}}` + "\n"
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(codexInput), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := run(t, "convert", "--promote", "codex:web-search", path)
	if err != nil {
		t.Fatal(err)
	}
	var s session.Session
	if err := json.Unmarshal([]byte(stdout), &s); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range s.Events {
		if ev.Kind == session.KindToolCall && ev.ToolCall.PromotedFrom == "event_msg/web_search_end" {
			found = true
		}
	}
	if !found {
		t.Errorf("no promoted tool_call in output:\n%s", stdout)
	}

	if _, _, err := run(t, "convert", "--promote", "not-a-promotion", path); err == nil {
		t.Error("unknown promotion: want error")
	}
	if _, _, err := run(t, "convert", "--promote", "codex:web-search", fixture); err == nil {
		t.Error("harness mismatch (claude-code transcript): want error")
	}
}

func TestDriftScanClean(t *testing.T) {
	stdout, _, err := run(t, "drift", "scan", fixture)
	if err != nil {
		t.Fatalf("%v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "clean") {
		t.Errorf("scan output = %q, want clean", stdout)
	}
}

func TestDriftScanDrifted(t *testing.T) {
	drifted := filepath.Join(t.TempDir(), "drifted.jsonl")
	line := `{"parentUuid":null,"isSidechain":false,"type":"user","message":{"role":"user","content":"hi"},"uuid":"u-1","timestamp":"2026-07-19T10:00:00.000Z","userType":"external","cwd":"/tmp","sessionId":"s-1","version":"2.1.197"}` + "\n" +
		`{"type":"hologram","uuid":"h-1"}` + "\n"
	if err := os.WriteFile(drifted, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := run(t, "drift", "scan", drifted)
	var ee *exitError
	if !errors.As(err, &ee) || ee.code != 1 {
		t.Fatalf("err = %v, want exit code 1\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "drift:") {
		t.Errorf("scan output = %q, want drift findings", stdout)
	}
}

func TestDriftHelpHidesMaintainerVerbs(t *testing.T) {
	stdout, _, err := run(t, "drift", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "scan") {
		t.Errorf("drift help must list scan:\n%s", stdout)
	}
	// probe and baseline are Hidden; the command list must not offer them.
	for _, hidden := range []string{"Probe installed harnesses", "Regenerate a vocabulary baseline"} {
		if strings.Contains(stdout, hidden) {
			t.Errorf("drift help leaks hidden verb %q:\n%s", hidden, stdout)
		}
	}
}

func TestDetectCommand(t *testing.T) {
	stdout, _, err := run(t, "detect", fixture)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "claude-code") || !strings.Contains(stdout, "certain") {
		t.Errorf("detect output = %q", stdout)
	}
}

func TestDetectUnrecognized(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "garbage.log")
	if err := os.WriteFile(path, []byte("not a transcript at all\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "detect", path); err == nil {
		t.Error("want error for unrecognized format")
	}
}

func TestConvertStdin(t *testing.T) {
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader(`{"parentUuid":null,"isSidechain":false,"type":"user","message":{"role":"user","content":"hi"},"uuid":"u-1","timestamp":"2026-07-19T10:00:00.000Z","userType":"external","entrypoint":"cli","cwd":"/tmp","sessionId":"s-1","version":"2.1.197","gitBranch":"main"}` + "\n"))
	root.SetArgs([]string{"convert", "-"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var s session.Session
	if err := json.Unmarshal(out.Bytes(), &s); err != nil {
		t.Fatalf("stdin convert output: %v", err)
	}
	if len(s.Events) != 2 {
		t.Errorf("got %d events, want 2", len(s.Events))
	}
}

// TestConvertBadFormatPreservesOutputFile pins that --format is validated
// before -o creates (and truncates) the output file.
func TestConvertBadFormatPreservesOutputFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "existing.json")
	if err := os.WriteFile(out, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "convert", "--format", "bogus", "-o", out, fixture); err == nil {
		t.Fatal("bad --format accepted")
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "precious" {
		t.Errorf("output file = %q, want untouched content", data)
	}
}
