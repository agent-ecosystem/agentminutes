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
	},
	harness.Codex: {
		discriminators: map[string][]string{
			"event_msg":     {"payload.type"},
			"response_item": {"payload.type", "payload.name"},
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
		rv.keys[k] = true
		var sub map[string]json.RawMessage
		if json.Unmarshal(v, &sub) == nil {
			for sk := range sub {
				rv.keys[k+"."+sk] = true
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
