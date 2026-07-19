package agentminutes

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"iter"
	"os"
	"slices"

	"github.com/agent-ecosystem/agentminutes/harness"
	"github.com/agent-ecosystem/agentminutes/harness/antigravity"
	"github.com/agent-ecosystem/agentminutes/harness/claudecode"
	"github.com/agent-ecosystem/agentminutes/harness/codex"
	"github.com/agent-ecosystem/agentminutes/session"
)

// SkipReasonRootMissing is the ScanOptions.OnSkip reason Scan reports for a
// registered locator whose default root does not exist on this machine.
// Consumers filtering skips match against this constant.
const SkipReasonRootMissing = "root does not exist"

// adapters is the explicit registry of supported harnesses, alphabetically.
// Registration is a visible list rather than init() side effects, per the
// loud-by-default design.
var adapters = []harness.Adapter{
	antigravity.Adapter{},
	claudecode.Adapter{},
	codex.Adapter{},
}

// Adapters returns the supported harness adapters.
func Adapters() []harness.Adapter { return slices.Clone(adapters) }

// AdapterFor returns the adapter for the given harness ID.
func AdapterFor(id harness.ID) (harness.Adapter, error) {
	return lookup(adapters, id)
}

// lookup finds the registry entry with the given harness ID.
func lookup[T interface{ ID() harness.ID }](registry []T, id harness.ID) (T, error) {
	for _, entry := range registry {
		if entry.ID() == id {
			return entry, nil
		}
	}
	var zero T
	return zero, fmt.Errorf("agentminutes: unsupported harness %q", id)
}

// Detect identifies the harness that wrote a transcript from its opening
// bytes (up to harness.SniffSize; a few KB usually suffice). It returns the
// best-matching adapter and that match's confidence; a NoMatch confidence
// means no adapter recognized the header and the adapter is nil. When two
// adapters grade a header equally, the first in the (alphabetical) registry
// wins.
func Detect(header []byte) (harness.Adapter, harness.Confidence) {
	var best harness.Adapter
	bestConf := harness.NoMatch
	for _, a := range adapters {
		if c := a.Sniff(header); c > bestConf {
			best, bestConf = a, c
		}
	}
	return best, bestConf
}

// Parse parses a native transcript into an accumulated Session using the
// identified harness's adapter. For streaming, obtain the adapter via
// AdapterFor (or Detect) and range over its Events directly. Transforms,
// when given, are applied to the event stream in order (e.g. the opt-in
// telemetry promotions exported by adapter packages).
func Parse(r io.Reader, id harness.ID, opts harness.Options, transforms ...session.Transform) (*session.Session, error) {
	a, err := AdapterFor(id)
	if err != nil {
		return nil, err
	}
	return harness.Parse(a, r, opts, transforms...)
}

// locators is the explicit registry of session discovery implementations,
// alphabetically. Every adapter currently implements harness.Locator; the
// list stays separate from adapters so the parsing and discovery contracts
// can diverge per harness if they ever need to.
var locators = []harness.Locator{
	antigravity.Adapter{},
	claudecode.Adapter{},
	codex.Adapter{},
}

// Locators returns the supported session discovery implementations.
func Locators() []harness.Locator { return slices.Clone(locators) }

// LocatorFor returns the locator for the given harness ID.
func LocatorFor(id harness.ID) (harness.Locator, error) {
	return lookup(locators, id)
}

// Scan enumerates session transcripts under every registered locator's
// default root. A default root that does not exist is reported via
// opts.OnSkip (reason SkipReasonRootMissing) and produces no error:
// scanning a machine that has only some harnesses installed is the normal
// case. All yielded errors are *harness.ScanError. For a non-default root,
// use LocatorFor(id).Scan directly.
func Scan(opts harness.ScanOptions) iter.Seq2[harness.SessionRef, error] {
	return func(yield func(harness.SessionRef, error) bool) {
		for _, l := range locators {
			root, err := l.DefaultRoot()
			if err != nil {
				if !yield(harness.SessionRef{}, &harness.ScanError{Harness: l.ID(), Err: err}) {
					return
				}
				continue
			}
			if _, err := os.Stat(root); err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					if opts.OnSkip != nil {
						opts.OnSkip(root, SkipReasonRootMissing)
					}
					continue
				}
				if !yield(harness.SessionRef{}, &harness.ScanError{Harness: l.ID(), Path: root, Err: err}) {
					return
				}
				continue
			}
			for ref, err := range l.Scan(root, opts) {
				if !yield(ref, err) {
					return
				}
			}
		}
	}
}

// Locate resolves a known session ID to its native transcript under the
// harness's default root. The error wraps os.ErrNotExist when no
// transcript for the ID exists.
func Locate(id harness.ID, sessionID string) (harness.SessionRef, error) {
	l, err := LocatorFor(id)
	if err != nil {
		return harness.SessionRef{}, err
	}
	root, err := l.DefaultRoot()
	if err != nil {
		return harness.SessionRef{}, err
	}
	return l.Locate(root, sessionID)
}
