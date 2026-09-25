// Package driftprobe implements the `agentminutes drift` devtool: probing
// installed harnesses for transcript-format drift, scanning existing
// transcripts against a vocabulary baseline, and regenerating baselines.
// See plans/drift-probe-design.md for the design record.
package driftprobe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/internal/parseutil"
)

// Baseline is the committed vocabulary of one harness's transcript format:
// record types, their key sets, and the values of churn-prone discriminator
// fields. It captures what strict parsing tolerates silently, so additive
// drift (new fields, new telemetry subtypes, new tool names) is detectable
// by diffing fresh transcripts against it.
type Baseline struct {
	Harness string `json:"harness"`

	// GeneratedFromVersion is informational; harness.LastValidated stays
	// the authoritative coverage record and a test asserts the two agree.
	GeneratedFromVersion string `json:"generated_from_version,omitempty"`

	RecordTypes map[string]*RecordVocab `json:"record_types"`
}

// RecordVocab is the observed vocabulary of one record type.
type RecordVocab struct {
	// Keys are the observed key paths, depth 2 (a top-level key, plus
	// key.subkey when the value is an object), sorted.
	Keys []string `json:"keys"`

	// Discriminators maps configured churn-prone paths (e.g. Codex's
	// "payload.type") to their observed values, sorted.
	Discriminators map[string][]string `json:"discriminators,omitempty"`
}

// vocabConfig says where one harness's format hides its churn.
type vocabConfig struct {
	// discriminators maps a record type ("*" = every type) to the paths
	// whose values are enumerable vocabulary. A path segment ending in []
	// iterates array elements.
	discriminators map[string][]string

	// normalize collapses unbounded user-specific values (e.g. MCP tool
	// names) so they never read as drift. Nil means identity.
	normalize func(path, value string) string

	// normalizeKey collapses key paths whose segments are per-record
	// identifiers (e.g. maps keyed by tool-use id) so every session does
	// not mint new vocabulary. Nil means identity.
	normalizeKey func(key string) string
}

// vocabConfigs, alphabetical like all harness lists. Tool-name paths are
// discriminators because a new tool name needs a toolKinds mapping in the
// adapter; MCP tool names are user config, not harness vocabulary, and are
// collapsed by normalize.
var vocabConfigs = map[harness.ID]vocabConfig{
	harness.Antigravity: {
		discriminators: map[string][]string{
			"*": {"source", "tool_calls[].name"},
		},
	},
	harness.ClaudeCode: {
		discriminators: map[string][]string{
			"assistant": {"message.content[].type", "message.content[].name"},
			"user":      {"message.content[].type"},
		},
		normalize: func(path, value string) string {
			if path == "message.content[].name" && strings.HasPrefix(value, "mcp__") {
				return "mcp__*"
			}
			return value
		},
		// 2.1.267 assistant records carry wireToolInputs and
		// wireIngestContext maps keyed by tool_use id, and cost-state
		// records a modelUsage map keyed by model name; the ids and model
		// names are the churn, the map names are the vocabulary.
		normalizeKey: func(key string) string {
			parent, child, ok := strings.Cut(key, ".")
			if ok && (strings.HasPrefix(child, "toolu_") || parent == "modelUsage") {
				return parent + ".*"
			}
			return key
		},
	},
	harness.Codex: {
		discriminators: map[string][]string{
			// payload.item.type/.kind: the 0.149+ item_completed stream
			// hides its churn in the typed item (FileChange, Extension of
			// kind web.search, ...), and the promotion transforms match on
			// exactly those values.
			"event_msg":     {"payload.type", "payload.item.type", "payload.item.kind"},
			"response_item": {"payload.type", "payload.name"},
		},
	},
	harness.Copilot: {
		discriminators: map[string][]string{
			// Tool names appear on the request (assistant.message) and
			// the execution record; the permission and error vocabularies
			// are the harness's own enumerations.
			"abort":                   {"data.reason"},
			"assistant.message":       {"data.toolRequests[].name", "data.reasoningBlocks.provider"},
			"permission.completed":    {"data.result.kind", "data.decisionSource"},
			"permission.requested":    {"data.permissionRequest.kind", "data.promptRequest.kind", "data.permissionMode", "data.agentMode"},
			"session.binary_asset":    {"data.type", "data.mimeType"},
			"session.info":            {"data.infoType"},
			"session.model_change":    {"data.source", "data.cause"},
			"session.shutdown":        {"data.shutdownType"},
			"skill.invoked":           {"data.source", "data.trigger"},
			"subagent.completed":      {"data.agentName"},
			"subagent.selected":       {"data.tools[]"},
			"subagent.started":        {"data.agentName", "data.agentType", "data.executionMode", "data.modelSelectionSource"},
			"tool.execution_complete": {"data.error.code"},
			"tool.execution_start":    {"data.toolName", "data.mcpConfigSource", "data.mcpTransport"},
			"user.message":            {"data.delivery", "data.source"},
		},
		// The built-in GitHub MCP server's tools are named
		// github-mcp-server-<tool>; their set is server config, not
		// harness vocabulary. A subagent prompt's source names the
		// parent session ("agent-<uuid>"): the prefix is the vocabulary.
		normalize: func(path, value string) string {
			if (path == "data.toolRequests[].name" || path == "data.toolName") && strings.HasPrefix(value, "github-mcp-server-") {
				return "github-mcp-server-*"
			}
			if path == "data.source" && strings.HasPrefix(value, "agent-") {
				return "agent-*"
			}
			return value
		},
	},
}

// vocabBuilder accumulates vocabulary as sets across many transcripts.
type vocabBuilder struct {
	id    harness.ID
	cfg   vocabConfig
	types map[string]*recordVocabBuilder
}

type recordVocabBuilder struct {
	keys map[string]bool
	disc map[string]map[string]bool
}

func newVocabBuilder(id harness.ID) (*vocabBuilder, error) {
	cfg, ok := vocabConfigs[id]
	if !ok {
		return nil, fmt.Errorf("no vocabulary config for harness %q", id)
	}
	return &vocabBuilder{id: id, cfg: cfg, types: map[string]*recordVocabBuilder{}}, nil
}

// addTranscript extracts the vocabulary of every line. A malformed JSON
// line is an error carrying the 1-based line, per the loud-failure rule.
func (b *vocabBuilder) addTranscript(data []byte) error {
	sc := parseutil.NewLineScanner(bytes.NewReader(data))
	for sc.Scan() {
		if err := b.addRecord(sc.Bytes()); err != nil {
			return fmt.Errorf("line %d: %w", sc.Line(), err)
		}
	}
	return sc.Err()
}

func (b *vocabBuilder) addRecord(raw []byte) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	rtype := stringField(obj, "type")
	if rtype == "" {
		rtype = "(untyped)"
	}
	rv := b.types[rtype]
	if rv == nil {
		rv = &recordVocabBuilder{keys: map[string]bool{}, disc: map[string]map[string]bool{}}
		b.types[rtype] = rv
	}
	for k, v := range obj {
		rv.keys[b.key(k)] = true
		var sub map[string]json.RawMessage
		if json.Unmarshal(v, &sub) == nil {
			for sk := range sub {
				rv.keys[b.key(k+"."+sk)] = true
			}
		}
	}
	for _, path := range append(b.cfg.discriminators[rtype], b.cfg.discriminators["*"]...) {
		for _, val := range pathValues(raw, path) {
			if b.cfg.normalize != nil {
				val = b.cfg.normalize(path, val)
			}
			set := rv.disc[path]
			if set == nil {
				set = map[string]bool{}
				rv.disc[path] = set
			}
			set[val] = true
		}
	}
	return nil
}

// key applies the harness's key normalization, if any.
func (b *vocabBuilder) key(k string) string {
	if b.cfg.normalizeKey != nil {
		return b.cfg.normalizeKey(k)
	}
	return k
}

func (b *vocabBuilder) finalize(version string) *Baseline {
	base := &Baseline{
		Harness:              string(b.id),
		GeneratedFromVersion: version,
		RecordTypes:          make(map[string]*RecordVocab, len(b.types)),
	}
	for rtype, rv := range b.types {
		out := &RecordVocab{Keys: sortedMapKeys(rv.keys)}
		if len(rv.disc) > 0 {
			out.Discriminators = make(map[string][]string, len(rv.disc))
			for path, set := range rv.disc {
				out.Discriminators[path] = sortedMapKeys(set)
			}
		}
		base.RecordTypes[rtype] = out
	}
	return base
}

// BuildBaseline extracts the union vocabulary of the given transcripts.
func BuildBaseline(id harness.ID, version string, transcripts [][]byte) (*Baseline, error) {
	b, err := newVocabBuilder(id)
	if err != nil {
		return nil, err
	}
	for i, data := range transcripts {
		if err := b.addTranscript(data); err != nil {
			return nil, fmt.Errorf("transcript %d: %w", i+1, err)
		}
	}
	return b.finalize(version), nil
}

// stringField returns obj[key] when it is a JSON string, else "".
func stringField(obj map[string]json.RawMessage, key string) string {
	var s string
	if raw, ok := obj[key]; ok && json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

// pathValues walks a dotted path ("payload.type"; a segment suffixed []
// iterates array elements, as in "message.content[].name") and collects the
// string values it reaches. Non-string leaves and absent segments yield
// nothing: discriminators enumerate vocabulary, they never fail a scan.
func pathValues(record json.RawMessage, path string) []string {
	values := []json.RawMessage{record}
	for seg := range strings.SplitSeq(path, ".") {
		iterate := strings.HasSuffix(seg, "[]")
		key := strings.TrimSuffix(seg, "[]")
		var next []json.RawMessage
		for _, v := range values {
			var m map[string]json.RawMessage
			if json.Unmarshal(v, &m) != nil {
				continue
			}
			child, ok := m[key]
			if !ok {
				continue
			}
			if !iterate {
				next = append(next, child)
				continue
			}
			var arr []json.RawMessage
			if json.Unmarshal(child, &arr) == nil {
				next = append(next, arr...)
			}
		}
		values = next
	}
	var out []string
	for _, v := range values {
		var s string
		if json.Unmarshal(v, &s) == nil {
			out = append(out, s)
		}
	}
	return out
}
