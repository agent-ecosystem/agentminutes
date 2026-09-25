// Package locatetest holds the shared accounting invariant for the
// env-gated local scan tests: the per-line accounting idea, lifted to
// per-file. It is test-only support code, not part of the library.
package locatetest

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/agent-ecosystem/agentminutes/harness"
)

// Invariant scans root with the locator and asserts that every regular
// file under root is accounted for as a ref path, a subagent path, a
// reported skip, or a scan error.
func Invariant(t *testing.T, l harness.Locator, root string) {
	t.Helper()
	accounted := make(map[string]string)
	var refs, errs int
	opts := harness.ScanOptions{OnSkip: func(path, reason string) { accounted[path] = "skip: " + reason }}
	for ref, err := range l.Scan(root, opts) {
		if err != nil {
			var se *harness.ScanError
			if !errors.As(err, &se) {
				t.Fatalf("scan yielded a non-ScanError: %v", err)
			}
			accounted[se.Path] = "error"
			errs++
			continue
		}
		refs++
		accounted[ref.Path] = "ref"
		for _, p := range ref.SubagentPaths {
			accounted[p] = "subagent"
		}
		if ref.Meta.SessionID == "" {
			t.Errorf("%s: ref has no session id", ref.Path)
		}
	}
	var files int
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		files++
		if _, ok := accounted[path]; !ok {
			t.Errorf("%s: not accounted for by scan", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("scanned %d files: %d refs, %d errors, %d total accounted", files, refs, errs, len(accounted))
}

// TaskInvariant scans root with the locator, gathers the task of every
// parent (non-subagent) ref, and asserts the grouping is a partition:
// every subagent transcript the scan discovers (as a subagent ref or a
// parent's SubagentPaths) is claimed by exactly one task, no task claims
// a transcript twice, and no task claims its own parent. It is the
// behavioral pin for the subagent join on real corpora; a harness whose
// children are unmarked (Antigravity) is checked for the claim-once and
// existence halves only.
func TaskInvariant(t *testing.T, l harness.Locator, root string) {
	t.Helper()
	var parents []harness.SessionRef
	discovered := map[string]string{} // subagent transcript -> its session id
	parentIDs := map[string]bool{}
	for ref, err := range l.Scan(root, harness.ScanOptions{}) {
		if err != nil {
			continue // ScanErrors are the scan invariant's concern
		}
		if ref.Meta.IsSubagent {
			discovered[ref.Path] = ref.Meta.SessionID
			continue
		}
		for _, p := range ref.SubagentPaths {
			discovered[p] = ref.Meta.SessionID
		}
		parents = append(parents, ref)
		parentIDs[ref.Meta.SessionID] = true
	}
	claimed := map[string]string{} // subagent path -> parent path
	var tasks, subagents int
	for _, parent := range parents {
		task, err := l.Gather(root, parent)
		if err != nil {
			t.Errorf("%s: gather: %v", parent.Path, err)
			continue
		}
		tasks++
		seen := map[string]bool{}
		for _, s := range task.Subagents {
			subagents++
			if s.Path == parent.Path {
				t.Errorf("%s: task claims its own parent", parent.Path)
			}
			if seen[s.Path] {
				t.Errorf("%s: task claims %s twice", parent.Path, s.Path)
			}
			seen[s.Path] = true
			if prev, ok := claimed[s.Path]; ok && prev != parent.Path {
				t.Errorf("%s: claimed by both %s and %s", s.Path, prev, parent.Path)
			}
			claimed[s.Path] = parent.Path
			if _, err := os.Stat(s.Path); err != nil {
				t.Errorf("%s: gathered subagent does not exist: %v", parent.Path, err)
			}
		}
	}
	var orphans int
	for p, sid := range discovered {
		if _, ok := claimed[p]; ok {
			continue
		}
		if !parentIDs[sid] {
			// The parent transcript is gone from the store: a fact about
			// the store, not the join.
			orphans++
			t.Logf("%s: subagent of %s, whose parent is not in the root (orphan)", p, sid)
			continue
		}
		t.Errorf("%s: subagent transcript claimed by no task although its parent %s is present", p, sid)
	}
	t.Logf("%d tasks, %d subagent transcripts gathered, %d discovered by scan (%d orphans), every non-orphan claimed once", tasks, subagents, len(discovered), orphans)
}
