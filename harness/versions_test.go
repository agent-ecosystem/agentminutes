package harness

import (
	"strings"
	"testing"
)

func TestVersionNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"2.2.3", "2.1.197", true},
		{"2.1.198", "2.1.197", true},
		{"2.1.197", "2.1.197", false},
		{"2.1.153", "2.1.197", false},
		{"3", "2.1.197", true},
		{"2.1", "2.1.197", false},
		{"2.1.197.1", "2.1.197", true},
		{"0.118.0-alpha.2", "0.144.1", false},
		{"0.145.0-alpha.2", "0.144.1", true},
		// Unparseable segments end the comparison as equal: a malformed
		// version must never trigger a false drift hint.
		{"dev", "1.1.1", false},
		{"1.1.1", "dev", false},
		{"", "1.1.1", false},
	}
	for _, c := range cases {
		if got := VersionNewer(c.a, c.b); got != c.want {
			t.Errorf("VersionNewer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestParseErrorDriftHint(t *testing.T) {
	newer := &ParseError{
		Harness:        ClaudeCode,
		HarnessVersion: "99.0.0",
		Line:           3,
		Msg:            `unrecognized record type "x"`,
	}
	msg := newer.Error()
	if !strings.Contains(msg, "newer than last validated "+LastValidated(ClaudeCode)) ||
		!strings.Contains(msg, "format drift") {
		t.Errorf("Error() = %q, want drift hint", msg)
	}
	if !strings.Contains(msg, "harness version 99.0.0") || !strings.Contains(msg, "at line 3") {
		t.Errorf("Error() = %q, want version and line", msg)
	}

	validated := &ParseError{Harness: ClaudeCode, HarnessVersion: LastValidated(ClaudeCode), Msg: "m"}
	if msg := validated.Error(); strings.Contains(msg, "format drift") {
		t.Errorf("Error() = %q, want no drift hint for a validated version", msg)
	}

	unknownHarness := &ParseError{Harness: ID("mystery"), HarnessVersion: "99.0.0", Msg: "m"}
	if msg := unknownHarness.Error(); strings.Contains(msg, "format drift") {
		t.Errorf("Error() = %q, want no drift hint without a LastValidated entry", msg)
	}

	noVersion := &ParseError{Harness: ClaudeCode, Msg: "m"}
	if msg := noVersion.Error(); strings.Contains(msg, "harness version") {
		t.Errorf("Error() = %q, want no version clause", msg)
	}
}
