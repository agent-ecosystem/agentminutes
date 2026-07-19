package harness

import (
	"errors"
	"os"
	"testing"
	"time"
)

func TestScanOptionsIncludes(t *testing.T) {
	at := func(s string) time.Time {
		ts, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return ts
	}
	started := at("2026-07-20T10:00:00Z")
	mod := at("2026-07-20T12:00:00Z")
	tests := []struct {
		name         string
		since, until string
		started      *time.Time
		mod          time.Time
		want         bool
	}{
		{"no window", "", "", &started, mod, true},
		{"inside window", "2026-07-20T00:00:00Z", "2026-07-21T00:00:00Z", &started, mod, true},
		{"ends before since", "2026-07-20T13:00:00Z", "", &started, mod, false},
		{"starts after until", "", "2026-07-20T09:00:00Z", &started, mod, false},
		{"straddles since", "2026-07-20T11:00:00Z", "", &started, mod, true},
		{"straddles until", "", "2026-07-20T11:00:00Z", &started, mod, true},
		{"no started, mod inside", "2026-07-20T11:00:00Z", "2026-07-20T13:00:00Z", nil, mod, true},
		{"no started, mod before since", "2026-07-20T13:00:00Z", "", nil, mod, false},
		{"mtime predates started (archived copy)", "", "2026-07-20T09:00:00Z", &mod, at("2026-07-20T08:00:00Z"), false},
		{"mtime predates started, window at start", "2026-07-20T11:30:00Z", "", &mod, at("2026-07-20T08:00:00Z"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := ScanOptions{}
			if tt.since != "" {
				opts.Since = at(tt.since)
			}
			if tt.until != "" {
				opts.Until = at(tt.until)
			}
			if got := opts.Includes(tt.started, tt.mod); got != tt.want {
				t.Errorf("Includes(%v, %v) with since=%q until=%q = %v, want %v",
					tt.started, tt.mod, tt.since, tt.until, got, tt.want)
			}
		})
	}
}

func TestCheckSessionID(t *testing.T) {
	for _, id := range []string{"s-1", "0199c9a3-b1de-7891-a7b2-cadf35eb2b83", "conv_A.2"} {
		if err := CheckSessionID(id); err != nil {
			t.Errorf("CheckSessionID(%q) = %v, want nil", id, err)
		}
	}
	for _, id := range []string{"", "../evil", "a/b", `a\b`, "x*", "x?", "x["} {
		if err := CheckSessionID(id); err == nil {
			t.Errorf("CheckSessionID(%q) = nil, want error", id)
		}
	}
}

func TestScanError(t *testing.T) {
	inner := os.ErrPermission
	err := &ScanError{Harness: ClaudeCode, Path: "/x/y.jsonl", Err: inner}
	if got := err.Error(); got != "claude-code: /x/y.jsonl: permission denied" {
		t.Errorf("Error() = %q", got)
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Error("ScanError does not unwrap to its cause")
	}
}
