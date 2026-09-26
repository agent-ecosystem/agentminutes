// Package textaudit holds the shared text-surfacing invariant for adapter
// tests: a system event whose Details carry a long string either surfaces
// that string as Text (the string contains, or is contained by, the
// text) or names the (subtype, path) on the adapter's enumerated non-text
// list, with a reason; a tool result is held to the same rule on its
// content text against its Enrichment (the sidecar), which is where the
// "which field did the model see" choice lives. It is the "never silently drop
// input" idea applied to model-visible text: a record's text that lives
// only in Details is invisible to consumers that trace phrases across a
// session (they read user_message content, tool_result content, and
// system.Text; never harness-shaped Details). The fixtures and the
// env-gated local corpus tests both run it, so a harness release that
// adds a text-bearing record (or moves the text to a new key) fails here
// instead of being absorbed as telemetry. Test-only support code, not
// part of the library.
package textaudit

import (
	"cmp"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/agent-ecosystem/agentminutes/session"
)

// MinLen is the string length from which a Details value counts as text
// rather than an identifier, name, or short path. Ids and hashes stay
// under it; prompts, bodies, and listings do not.
const MinLen = 80

// Rule names the invariant a Finding violates.
type Rule string

const (
	// Untexted: the event surfaces no text at all, and its details carry
	// a long string.
	Untexted Rule = "untexted"
	// Uncontained: the event surfaces text, and its details carry a long
	// string that neither contains nor is contained by that text, so it
	// is a second text the event does not surface.
	Uncontained Rule = "uncontained"
)

// Finding is one (subtype, path) pair at which an event's details carry
// a string of at least MinLen bytes that the event's text does not
// account for.
type Finding struct {
	// Subtype is the system event's subtype, or "tool_result/<tool name>"
	// for a tool result (whose details are its enrichment).
	Subtype string
	// Path is the JSON path inside the details, segments joined by ".",
	// with "[]" for array elements ("files.[].content") and "$" when the
	// details are a bare string.
	Path string
	// Len is the longest such string seen at the path; Line is the
	// provenance line of the event that carried it.
	Len  int
	Line int
	Rule Rule
}

// Key is the allowlist form: "<subtype> <path>".
func (f Finding) Key() string { return f.Subtype + " " + f.Path }

// Audit returns the findings over events, one per (subtype, path), sorted
// by key. System events are audited on Text against Details; tool results
// on their content text against Enrichment.
func Audit(events []session.Event) []Finding {
	byKey := make(map[string]Finding)
	for i := range events {
		ev := &events[i]
		var subtype, text string
		var details json.RawMessage
		switch {
		case ev.Kind == session.KindSystem && ev.System != nil:
			subtype, text, details = ev.System.Subtype, ev.System.Text, ev.System.Details
		case ev.Kind == session.KindToolResult && ev.ToolResult != nil:
			subtype, text, details = "tool_result/"+ev.ToolResult.ToolName, ev.ToolResult.Text(), ev.ToolResult.Enrichment
		default:
			continue
		}
		if len(details) == 0 {
			continue
		}
		var v any
		if err := json.Unmarshal(details, &v); err != nil {
			continue
		}
		walk(v, nil, func(path []string, s string) {
			rule := Untexted
			if text != "" {
				if strings.Contains(s, text) || strings.Contains(text, s) {
					return
				}
				rule = Uncontained
			}
			f := Finding{Subtype: subtype, Path: cmp.Or(strings.Join(path, "."), "$"), Len: len(s), Rule: rule}
			if ev.Provenance != nil {
				f.Line = ev.Provenance.Line
			}
			if prev, ok := byKey[f.Key()]; !ok || prev.Len < f.Len {
				byKey[f.Key()] = f
			}
		})
	}
	out := make([]Finding, 0, len(byKey))
	for _, f := range byKey {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}

func walk(v any, path []string, visit func(path []string, s string)) {
	switch x := v.(type) {
	case string:
		if len(x) >= MinLen {
			visit(path, x)
		}
	case map[string]any:
		for k, child := range x {
			walk(child, append(path, k), visit)
		}
	case []any:
		for _, child := range x {
			walk(child, append(path, "[]"), visit)
		}
	}
}

// Invariant asserts that every finding over events is allowed. allow maps
// a Finding.Key() to the reason the string is not model-visible text (or
// is text already surfaced elsewhere); the reason is documentation, kept
// next to the decision. A "*" path segment in a key matches one or more
// segments, so "tool.execution_start arguments.*" covers every argument
// key; a subtype ending in "/*" matches every subtype under that prefix
// ("attachment/* cwd"). name labels the failure (the fixture or
// transcript path).
func Invariant(t testing.TB, name string, events []session.Event, allow map[string]string) {
	t.Helper()
	for _, f := range Audit(events) {
		if allowed(f, allow) {
			continue
		}
		switch f.Rule {
		case Untexted:
			t.Errorf("%s:%d: event %q carries a %d-byte string at %q and surfaces no text; surface it or add %q to the adapter's non-text list with a reason",
				name, f.Line, f.Subtype, f.Len, f.Path, f.Key())
		default:
			t.Errorf("%s:%d: event %q carries a %d-byte string at %q that its text neither contains nor is contained by (a second text); surface it or add %q to the adapter's non-text list with a reason",
				name, f.Line, f.Subtype, f.Len, f.Path, f.Key())
		}
	}
}

func allowed(f Finding, allow map[string]string) bool {
	if _, ok := allow[f.Key()]; ok {
		return true
	}
	segs := strings.Split(f.Path, ".")
	for key := range allow {
		subtype, pattern, ok := strings.Cut(key, " ")
		if !ok || !subtypeMatches(subtype, f.Subtype) {
			continue
		}
		if pattern == f.Path || (strings.Contains(pattern, "*") && match(strings.Split(pattern, "."), segs)) {
			return true
		}
	}
	return false
}

func subtypeMatches(pattern, subtype string) bool {
	if prefix, ok := strings.CutSuffix(pattern, "/*"); ok {
		return strings.HasPrefix(subtype, prefix+"/")
	}
	return pattern == subtype
}

// match reports whether pattern matches segs, with "*" standing for one or
// more consecutive segments (so a key containing dots, such as a model
// name, is still one wildcard).
func match(pattern, segs []string) bool {
	if len(pattern) == 0 {
		return len(segs) == 0
	}
	if pattern[0] == "*" {
		for i := 1; i <= len(segs); i++ {
			if match(pattern[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	return len(segs) > 0 && pattern[0] == segs[0] && match(pattern[1:], segs[1:])
}
