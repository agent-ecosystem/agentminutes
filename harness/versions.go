package harness

import (
	"strconv"
	"strings"
)

// lastValidated is the data behind LastValidated. Alphabetical, like all
// harness lists; update the entry when an adapter's inventory is
// re-validated against a newer release.
var lastValidated = map[ID]string{
	Antigravity: "1.1.3",
	ClaudeCode:  "2.1.205",
	Codex:       "0.144.1",
}

// LastValidated returns the newest release of the harness whose transcript
// format its adapter was validated against (the observed version in the
// empirical inventory under plans/), or "" for an unknown harness. It is
// coverage documentation made machine-readable, not a compatibility bound:
// parsing is shape-based and usually survives newer releases. Its one
// behavioral use is the drift hint ParseError.Error appends when a failing
// transcript self-declares a newer version.
func LastValidated(id ID) string { return lastValidated[id] }

// VersionNewer reports whether version a is strictly newer than b,
// comparing dot-separated segments numerically by their leading digits
// (missing segments count as zero). It is deliberately lenient: harness
// versions are not guaranteed semver, and an unparseable segment ends the
// comparison as equal, so a malformed version never triggers a false drift
// hint.
func VersionNewer(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		av, aok := segmentNumber(as, i)
		bv, bok := segmentNumber(bs, i)
		if !aok || !bok {
			return false
		}
		if av != bv {
			return av > bv
		}
	}
	return false
}

// segmentNumber returns the numeric value of the i-th version segment: the
// segment's leading digits (so "0-alpha" reads as 0), or zero when past the
// end. ok is false when the segment exists but starts with no digit.
func segmentNumber(segs []string, i int) (n int, ok bool) {
	if i >= len(segs) {
		return 0, true
	}
	s := segs[i]
	j := 0
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j == 0 {
		return 0, false
	}
	v, err := strconv.Atoi(s[:j])
	if err != nil {
		return 0, false
	}
	return v, true
}
