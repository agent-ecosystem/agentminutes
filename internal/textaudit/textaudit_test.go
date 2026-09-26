package textaudit

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/agent-ecosystem/agentminutes/session"
)

func sys(subtype, text string, details string) session.Event {
	return session.Event{Kind: session.KindSystem, System: &session.SystemEvent{
		Subtype: subtype, Text: text, Details: json.RawMessage(details),
	}}
}

func TestAuditFindsLongStringsOnlyWhenTextEmpty(t *testing.T) {
	long := strings.Repeat("x", MinLen)
	other := strings.Repeat("y", MinLen)
	events := []session.Event{
		sys("a", "", `{"body":"`+long+`","id":"short","nested":{"items":[{"t":"`+long+`y"}]}}`),
		// Surfaced text: contained (wrapper) and containing (fragment) strings pass; a distinct one is a second text.
		sys("a", long, `{"body":"<w>`+long+`</w>","frag":"`+long[:MinLen]+`","second":"`+other+`"}`),
		sys("b", "", `{"n":1,"s":"`+long+`"}`),
		{Kind: session.KindUserMessage, UserMessage: &session.UserMessage{}},
		{Kind: session.KindToolResult, ToolResult: &session.ToolResult{
			ToolName:   "Read",
			Content:    []session.ContentBlock{{Kind: session.ContentText, Text: "1\t" + long}},
			Enrichment: json.RawMessage(`{"file":{"content":"` + long + `","raw":"` + other + `"}}`),
		}},
	}
	got := Audit(events)
	want := []Finding{
		{Subtype: "a", Path: "body", Len: MinLen, Rule: Untexted},
		{Subtype: "a", Path: "nested.items.[].t", Len: MinLen + 1, Rule: Untexted},
		{Subtype: "a", Path: "second", Len: MinLen, Rule: Uncontained},
		{Subtype: "b", Path: "s", Len: MinLen, Rule: Untexted},
		{Subtype: "tool_result/Read", Path: "file.raw", Len: MinLen, Rule: Uncontained},
	}
	if len(got) != len(want) {
		t.Fatalf("Audit = %+v, want %+v", got, want)
	}
	for i := range want {
		got[i].Line = 0
		if got[i] != want[i] {
			t.Errorf("Audit[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestAllowedWildcards(t *testing.T) {
	allow := map[string]string{
		"t exact":              "",
		"t args.*":             "",
		"t models.*.call_id":   "",
		"u prefix.*.suffix.[]": "",
		"w/* cwd":              "",
		"w/* args.*":           "",
	}
	cases := []struct {
		key  string
		want bool
	}{
		{"t exact", true},
		{"t exact.more", false},
		{"t args.command", true},
		{"t args.a.b.c", true},
		{"t args", false},
		{"t models.gpt-5.4.call_id", true},
		{"t models.call_id", false},
		{"u prefix.x.suffix.[]", true},
		{"u prefix.x.suffix", false},
		{"v args.command", false},
		{"w/x cwd", true},
		{"w/x/y cwd", true},
		{"w cwd", false},
		{"w/x args.a", true},
	}
	for _, c := range cases {
		subtype, path, _ := strings.Cut(c.key, " ")
		if got := allowed(Finding{Subtype: subtype, Path: path}, allow); got != c.want {
			t.Errorf("allowed(%q) = %v, want %v", c.key, got, c.want)
		}
	}
}

func TestInvariantReportsUnallowed(t *testing.T) {
	long := strings.Repeat("x", MinLen)
	events := []session.Event{sys("a", "", `{"body":"`+long+`","note":"`+long+`"}`)}
	var rec recorder
	Invariant(&rec, "fixture", events, map[string]string{"a note": "a note"})
	if len(rec.errs) != 1 || !strings.Contains(rec.errs[0], `"a body"`) {
		t.Errorf("Invariant errors = %q, want one naming \"a body\"", rec.errs)
	}
}

type recorder struct {
	testing.TB
	errs []string
}

func (r *recorder) Helper() {}
func (r *recorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}
