# Session discovery design

Status: **built** (`harness.Locator` + per-adapter `locate.go` + facade
`Scan`/`Locate`/`LocatorFor` + `agentminutes sessions` + `internal/locatetest`
invariant). Two contract details changed during implementation and are
reconciled in the text below:

- The identity read is **uncapped**, not budgeted: parsers already bound
  line size and the read stops at the first event, so a byte cap only added
  a false-failure mode for legitimate transcripts with large first records.
  The worst case (a pattern-matching file whose head yields no meta) is one
  bounded-by-file-size read that fails loudly.
- Files excluded by a Since/Until window are **exempt from per-file
  accounting**: pruned subtrees (Codex date directories) are never walked
  at all, which is the point of the window. Scan without a window for full
  accounting; the local-scan invariant tests do.

Validated on the real corpora on this machine: Claude Code 180 files / 22
sessions, Codex 11/11, Antigravity 1503 files / 18 conversations — every
regular file accounted, zero errors.

## Problem

Native transcripts land in global, harness-owned locations with no notion of
"the sessions belonging to project/experiment X". Consumers (first: the
harness-llmify experiment's runner and analysis phase) need two operations
the library doesn't offer:

1. **Locate at capture time**: a runner just invoked a harness headlessly and
   knows the session ID (or controls the cwd); it needs the native transcript
   path(s) to archive, including subagent files.
2. **Filter post hoc**: given the harness's whole transcript root, enumerate
   sessions cheaply and select by cwd, session ID, or time window, then feed
   the survivors to `convert`.

Both reduce to the same missing capability: per-harness **layout knowledge**
(where transcripts live, what identity can be read cheaply) exported as API.
Today that knowledge exists only in the env-gated `TestLocalTranscripts`
walks and the inventory docs.

## Non-goals

- **No experiment tagging.** There is no reliable way to inject a tag into a
  native transcript without perturbing the harness, and inventing one would
  violate the translate-don't-judge posture. Experiment identity belongs to
  the consumer (e.g. a runner's archive manifest); our job is making sessions
  findable by the keys harnesses actually record: session ID, cwd, time.
- **No watching, indexing, or caching.** On-demand scan only, consistent with
  the drift tool's no-cron, no-passive-sweep stance.
- **No parsing beyond identity.** A scan reads each candidate's head (and
  stat) only; `Stats()`/full parse remain separate, downstream steps.
- **No cross-copy dedup.** A transcript archived to two places is two refs.

## What each harness gives us

| | Claude Code | Codex | Antigravity |
| --- | --- | --- | --- |
| Root | `~/.claude/projects` | `~/.codex/sessions` | `~/.gemini/antigravity-cli/brain` |
| Layout | `<cwd-slug>/<session-uuid>.jsonl`; subagents at `<cwd-slug>/<session-uuid>/subagents/agent-*.jsonl` | `YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl` | `<conversation-id>/.system_generated/logs/transcript_full.jsonl` |
| Session ID | filename (confirmed in-band per record) | filename uuid (confirmed by `session_meta`) | **directory name only** (nothing in-band) |
| CWD | in-band per record; dir name is a lossy encoding (`/` and `.` both become `-`), so match on the in-band value, never the slug | in-band (`session_meta.cwd`) | **absent** |
| Started at | first record timestamp | `session_meta` timestamp | first record `CreatedAt` |
| Harness version | per record | `session_meta.cli_version` | absent |
| Time-pruning | none (walk everything) | date directories prune `--since`/`--until` walks | none |

Consequences baked into the design:

- Identity is a cheap header read everywhere the format records it, because
  `Meta` is by contract the first event of every stream.
- Antigravity identity is **layout-derived**: discovery fills the session ID
  from the conversation directory name (exactly what the parser's sparse-meta
  comment points at as the out-of-band source). Antigravity refs have no cwd,
  so cwd filtering excludes them loudly (counted, not silent).
- Claude Code discovery must attach the `subagents/` subtree to the parent
  ref; forgetting those files is the likeliest consumer bug, so the API
  carries them, not a doc footnote.

## API

### `harness` package: the contract

Discovery is a new, separate interface, not an extension of `Adapter`:
`Adapter` is deliberately `io.Reader`-based with no filesystem knowledge, and
parsing a stream must never require one.

```go
// Locator is the discovery counterpart to Adapter: it knows where a harness
// writes session transcripts and how to cheaply read identity from them.
type Locator interface {
	// ID identifies the harness this locator discovers.
	ID() ID

	// DefaultRoot returns the harness's conventional transcript root for
	// the current user (e.g. ~/.claude/projects). It does not check that
	// the directory exists.
	DefaultRoot() (string, error)

	// Scan enumerates session transcripts under root in walk order. Every
	// regular file under root is accounted for as exactly one of: part of
	// a yielded ref (main or subagent transcript), a reported skip
	// (opts.OnSkip), or a yielded *ScanError — except files excluded by a
	// Since/Until window, which are omitted without notice (pruned
	// subtrees are never walked). Unlike Adapter.Events, a yielded error
	// does not end the sequence: it identifies one unreadable or
	// unidentifiable expected-transcript file, and the scan continues.
	Scan(root string, opts ScanOptions) iter.Seq2[SessionRef, error]

	// Locate resolves a known session ID under root without a full scan,
	// using layout-level matching (filename or directory name) confirmed
	// by an identity read. os.ErrNotExist-wrapped error when absent.
	Locate(root, sessionID string) (SessionRef, error)
}

// ScanOptions control a Scan.
type ScanOptions struct {
	// Since/Until, when non-zero, keep only sessions whose activity
	// interval [StartedAt, ModTime] overlaps the window. Locators may use
	// layout knowledge to prune the walk (Codex date directories); the
	// filter itself is authoritative either way.
	Since, Until time.Time

	// OnSkip, when set, is called once per file excluded per the locator's
	// documented layout rules (non-transcript files, sidecar assets), with
	// the path and a stable reason string.
	OnSkip func(path, reason string)
}

// SessionRef identifies one discovered native session transcript.
type SessionRef struct {
	// Meta is the identity read from the transcript head, enriched with
	// layout-derived identity where the format is sparse in-band
	// (Antigravity's SessionID comes from the conversation directory
	// name). Enrichments are documented per locator.
	Meta session.Meta `json:"meta"`

	// Path is the main transcript file.
	Path string `json:"path"`

	// SubagentPaths are transcripts of harness-spawned subagents belonging
	// to this session (Claude Code agent-*.jsonl), each a separate parse
	// input. A subagent file whose parent transcript is missing is yielded
	// as its own ref (Meta.IsSubagent set) rather than dropped.
	SubagentPaths []string `json:"subagent_paths,omitempty"`

	// StartedAt is the first in-band timestamp, when the head carries one.
	StartedAt *time.Time `json:"started_at,omitempty"`

	// ModTime is the file's mtime: a proxy for session end that copying
	// or archiving can falsify. No in-band end timestamp is read in v1
	// (that would mean tail-reading every candidate).
	ModTime time.Time `json:"mod_time"`

	// SizeBytes is the main transcript's size.
	SizeBytes int64 `json:"size_bytes"`
}
```

`SessionRef` is a discovery-layer type, not part of the session JSON
contract: no `schema:` tags, no `SchemaVersion` coupling, and its JSON shape
may change with a CLI release note rather than a schema review.

### Per-adapter implementations

`harness/<name>/locate.go` in each adapter package; the adapter struct
itself implements `Locator` (one exported type per harness keeps the facade
registry shape unchanged).

Identity reads go **through the adapter's own `Events` stream**
(`harness.ReadIdentity`), taking the leading `KindSessionMeta` event (and
its timestamp for `StartedAt`) and then breaking. No second per-format
header parser to drift out of sync; sniffing and loud `ParseError`s come
for free. Costs accepted: Claude Code and Codex stop after the first
model-visible record; the Antigravity parser buffers the whole file before
emitting, which is acceptable at one transcript per conversation directory
(revisit only if real brain dirs prove large). The read is uncapped — see
the status note at the top for why the designed byte budget was dropped. A
head that yields no meta (empty file, wrong format under the right name) is
a yielded error, not a skip.

Accounting rule, per file under root:

- Matches the layout's transcript pattern and reads cleanly → part of a ref.
- Matches the pattern but is unreadable or unidentifiable → yielded error
  (scan continues). Loud, because a file shaped like a transcript that we
  cannot identify is exactly the drift/corruption signal this library exists
  to surface.
- Does not match the pattern (Antigravity sidecar files and `steps/<n>/`
  assets, editor droppings, non-`.jsonl` files) → `OnSkip` with a stable
  reason. Free when `OnSkip` is unset.

### Facade

```go
// Locators returns the registered locators, alphabetically by harness.
func Locators() []harness.Locator

// LocatorFor returns the locator for the given harness ID.
func LocatorFor(id harness.ID) (harness.Locator, error)

// Scan enumerates sessions under every registered locator's default root.
// A default root that does not exist is reported via opts.OnSkip (reason
// "root does not exist") and produces no error: scanning a machine that has
// only some harnesses installed is the normal case.
func Scan(opts harness.ScanOptions) iter.Seq2[harness.SessionRef, error]

// Locate resolves a session ID under a harness's default root.
func Locate(id harness.ID, sessionID string) (harness.SessionRef, error)
```

The locator registry is an explicit alphabetical list next to `adapters`,
per the visible-registration convention. Non-default roots go through
`LocatorFor(id).Scan(root, opts)`.

Filtering beyond the time window is deliberately **not** in `ScanOptions`:
matching on cwd, session ID, or git branch is plain code over `SessionRef`
and needs no per-harness knowledge. Only Since/Until sit in the contract,
because only they can prune a walk.

## CLI

```
agentminutes sessions [flags]

  --harness      restrict to one harness (default: scan all default roots)
  --root         explicit transcript root (requires --harness)
  --cwd          keep sessions whose recorded cwd equals this path (cleaned);
                 sessions with no recorded cwd (Antigravity) are excluded and
                 counted in the stderr summary
  --session-id   keep the session with this ID (uses Locate when --harness
                 is also given)
  --since/--until  RFC 3339 timestamps bounding the activity window
  --format       paths (default) | json | jsonl
```

`paths` prints one transcript path per line, subagent paths included, so the
output composes: `agentminutes sessions --cwd ~/runs/exp-42 | xargs -n1
agentminutes convert`. `json` emits the ref array; `jsonl` streams one ref
per line. A summary line on stderr reports refs matched, refs filtered out
(by which filter), skips, and per-file errors, mirroring `convert`'s
accounting posture. Per-file errors go to stderr and set exit status 1
without aborting the scan; zero matches with no errors exits 0 with empty
output (empty is an answer, not a failure).

Flag list, help text, and any harness enumeration stay alphabetical.

## The capture-time story (consumer-side, for the experiment)

Discovery makes the runner's archive step mechanical, per harness:

- **Claude Code**: capture `session_id` from `--output-format json` (or pin
  it with `--session-id`), then `Locate(claude-code, id)` returns the main
  path plus `SubagentPaths` — copy all of them.
- **Codex**: capture the session ID from `codex exec` output, `Locate`.
- **Antigravity**: nothing in-band and cwd is not recorded, so the runner
  must capture the conversation ID at invocation time (smoke-test item in
  the experiment design: confirm a headless `agy` run surfaces it; fallback
  is `Scan` with a tight mtime window around the invocation).

Post-hoc filtering by `--cwd` works for Claude Code and Codex whenever the
runner gives each experiment (or run) its own working directory, which the
experiment's fs condition already does.

## Testing

- **Hermetic layout fixtures**: a synthetic root per harness under
  `harness/<name>/testdata/` (LF-only, synthetic content per the fixture
  rules) exercising: ref + subagent attachment, orphan subagent, skip
  reasons, expected-transcript error continuation, Codex date pruning,
  Antigravity dirname-derived session ID.
- **Env-gated local scans**: `TestLocalScan` per adapter, reusing the
  existing `AGENTMINUTES_LOCAL_*` roots. Cross-check: every `.jsonl` the
  `TestLocalTranscripts` walk finds must be accounted for by `Scan` as ref
  path, subagent path, skip, or error — the same per-line accounting idea,
  lifted to per-file.
- **CLI golden tests** in the `main_test.go` style over the fixture roots.

## Follow-through when built

Update the CLAUDE.md layout line and commands, README (CLI section + any
harness capability table), and DEVELOPMENT.md if adapter authors gain a
"implement the Locator" step. New harness checklists must include layout
knowledge alongside the format inventory.

## Deferred / revisit triggers

- **In-band end timestamp** (bounded tail read) if mtime-as-end proves too
  lossy for archived corpora. The field would replace `ModTime` as the
  filter bound, not sit beside it silently.
- **Git-branch filtering** in the CLI: the data is already on `Meta`; add a
  flag when a consumer asks.
- **`--cwd` prefix matching** (subtree selection) if exact-match proves too
  rigid for runners that nest per-run directories; exact match first because
  it has no false positives.
- **Antigravity workspace recovery**: if a future Antigravity release
  records the `--add-dir` workspace in-band, lift it into `Meta.CWD` and
  delete the cwd-filter exclusion for that harness.
