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
