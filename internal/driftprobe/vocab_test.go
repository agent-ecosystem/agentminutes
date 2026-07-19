package driftprobe

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-ecosystem/agentminutes/harness"
)

const (
	// __WORKDIR__ is a placeholder the probe tests' fake harness replaces
	// with the probe workdir (a verbatim cwd would read as a foreign
	// session's transcript); the vocabulary tests use it as an opaque value.
	ccUser  = `{"parentUuid":null,"isSidechain":false,"type":"user","message":{"role":"user","content":[{"type":"text","text":"hi"}]},"uuid":"u-1","timestamp":"2026-07-19T10:00:00.000Z","userType":"external","cwd":"__WORKDIR__","sessionId":"s-1","version":"2.1.197"}`
	ccTools = `{"parentUuid":"u-1","isSidechain":false,"type":"assistant","message":{"id":"m-1","role":"assistant","model":"x","content":[{"type":"tool_use","id":"t-1","name":"Bash","input":{}},{"type":"tool_use","id":"t-2","name":"mcp__server__thing","input":{}}]},"uuid":"a-1","timestamp":"2026-07-19T10:00:01.000Z","sessionId":"s-1","version":"2.1.197"}`
)

func TestBuildBaselineVocabulary(t *testing.T) {
	b, err := BuildBaseline(harness.ClaudeCode, "2.1.197", [][]byte{[]byte(ccUser + "\n" + ccTools + "\n")})
	if err != nil {
		t.Fatal(err)
	}
	if b.Harness != "claude-code" || b.GeneratedFromVersion != "2.1.197" {
		t.Errorf("baseline identity = %q/%q", b.Harness, b.GeneratedFromVersion)
	}
	user := b.RecordTypes["user"]
	if user == nil || !slices.Contains(user.Keys, "message.role") || !slices.Contains(user.Keys, "uuid") {
		t.Fatalf("user vocab = %+v, want depth-2 keys", user)
	}
	names := b.RecordTypes["assistant"].Discriminators["message.content[].name"]
	if !slices.Contains(names, "Bash") || !slices.Contains(names, "mcp__*") {
		t.Errorf("tool names = %v, want Bash and normalized mcp__*", names)
	}
	if slices.Contains(names, "mcp__server__thing") {
		t.Errorf("tool names = %v: MCP names are user config and must be collapsed", names)
	}
}

func TestDiffBaseline(t *testing.T) {
	base, err := BuildBaseline(harness.ClaudeCode, "", [][]byte{[]byte(ccUser + "\n" + ccTools + "\n")})
	if err != nil {
		t.Fatal(err)
	}

	if d := DiffBaseline(base, base); d.HasDrift() || len(d.Unexercised) != 0 {
		t.Errorf("self-diff = %+v, want empty", d)
	}

	drifted := ccDriftRecord + "\n" +
		`{"parentUuid":null,"isSidechain":false,"type":"user","flux":9,"message":{"role":"user","content":[{"type":"text","text":"hi"}]},"uuid":"u-2","timestamp":"2026-07-19T10:00:02.000Z","userType":"external","cwd":"/tmp","sessionId":"s-1","version":"2.1.197"}` + "\n" +
		`{"parentUuid":"u-2","isSidechain":false,"type":"assistant","message":{"id":"m-2","role":"assistant","model":"x","content":[{"type":"tool_use","id":"t-3","name":"Teleport","input":{}}]},"uuid":"a-2","timestamp":"2026-07-19T10:00:03.000Z","sessionId":"s-1","version":"2.1.197"}` + "\n"
	observed, err := BuildBaseline(harness.ClaudeCode, "", [][]byte{[]byte(drifted)})
	if err != nil {
		t.Fatal(err)
	}
	d := DiffBaseline(base, observed)
	if !d.HasDrift() {
		t.Fatal("want drift")
	}
	if !slices.Equal(d.NewRecordTypes, []string{"hologram"}) {
		t.Errorf("NewRecordTypes = %v", d.NewRecordTypes)
	}
	if !slices.Contains(d.NewKeys["user"], "flux") {
		t.Errorf("NewKeys[user] = %v, want flux", d.NewKeys["user"])
	}
	if got := d.NewDiscriminators["assistant"]["message.content[].name"]; !slices.Contains(got, "Teleport") {
		t.Errorf("new tool names = %v, want Teleport", got)
	}

	var buf bytes.Buffer
	d.write(&buf)
	for _, want := range []string{`new record type "hologram"`, `new key "flux"`, `new message.content[].name value "Teleport"`} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("report %q missing %q", buf.String(), want)
		}
	}
}

// TestEmbeddedBaselines keeps the shipped baselines in step with the code:
// one per registered vocabulary config, agreeing with harness.LastValidated.
func TestEmbeddedBaselines(t *testing.T) {
	for id := range vocabConfigs {
		b, err := LoadBaseline(id)
		if err != nil {
			t.Errorf("%s: %v", id, err)
			continue
		}
		if b.Harness != string(id) {
			t.Errorf("%s: baseline harness = %q", id, b.Harness)
		}
		if b.GeneratedFromVersion != harness.LastValidated(id) {
			t.Errorf("%s: baseline generated from %q, LastValidated is %q; regenerate the baseline",
				id, b.GeneratedFromVersion, harness.LastValidated(id))
		}
		if len(b.RecordTypes) == 0 {
			t.Errorf("%s: baseline is empty", id)
		}
	}
}

// TestScanFixturesClean pins the invariant that makes drift findings
// meaningful: the committed fixtures scan clean against the committed
// baselines.
func TestScanFixturesClean(t *testing.T) {
	fixtures := map[string]string{
		"antigravity":                 filepath.Join("..", "..", "harness", "antigravity", "testdata", "transcript_full.jsonl"),
		"claude-code":                 filepath.Join("..", "..", "harness", "claudecode", "testdata", "session.jsonl"),
		"claude-code-search-fallback": filepath.Join("..", "..", "harness", "claudecode", "testdata", "search_fallback.jsonl"),
		"codex":                       filepath.Join("..", "..", "harness", "codex", "testdata", "rollout.jsonl"),
	}
	for name, path := range fixtures {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if cat := Scan(&buf, path, data); cat != Clean {
			t.Errorf("%s: category %v, want clean; report:\n%s", name, cat, buf.String())
		}
	}
}

func TestScanDrifted(t *testing.T) {
	drifted := ccUser + "\n" + ccDriftRecord + "\n"
	var buf bytes.Buffer
	if cat := Scan(&buf, "drifted.jsonl", []byte(drifted)); cat != Drift {
		t.Fatalf("category %v, want drift; report:\n%s", cat, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "strict parse failed") || !strings.Contains(out, `new record type "hologram"`) {
		t.Errorf("report missing parse failure or vocabulary drift:\n%s", out)
	}
}

func TestScanUndetectable(t *testing.T) {
	var buf bytes.Buffer
	if cat := Scan(&buf, "x", []byte("not a transcript\n")); cat != ExecError {
		t.Errorf("category %v, want exec error", cat)
	}
}
