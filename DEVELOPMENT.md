# Development guide

How to extend agentminutes, and in particular how to add a harness adapter. This process was developed while building the first three adapters (Claude Code, Codex CLI, Antigravity CLI); every rule here exists because skipping it bit us at least once.

## Ground rules

These are the library's contract, and adapters must uphold all of them:

1. **Loud failure.** An unrecognized record or malformed structure is a `*harness.ParseError` carrying harness ID, harness version (when known), and 1-based line number. Never silently skip anything.
2. **Every input line is accounted for.** Each line becomes a normalized event, a documented skip (reported via `Options.OnSkip`, counted in the session report), or an error. This is enforced mechanically by line-accounting tests, not by review.
3. **Streaming is the core.** Adapters implement `Events(io.Reader, Options) iter.Seq2[session.Event, error]`; whole-file parsing is the accumulator over it. If a format forces buffering (out-of-order records, single-document files), buffer internally and document that the adapter does not stream in constant memory.
4. **The first event is always `session_meta`**, even when the format carries little identity (emit a sparse Meta rather than none).
5. **Content is verbatim.** Never de-template, reflow, or "clean up" payloads. Truncation happens only via `Options.MaxPayloadBytes`, replacing payloads with size-and-digest placeholders.
6. **Options semantics are uniform**: `Permissive` turns unclassifiable records into `unknown` events preserving the raw record; `KeepRaw` retains verbatim source records in event provenance; `OnSkip` fires for every skip-listed record.

## Adding a harness adapter

### Step 0: generate fresh ground truth

Do not trust transcripts already on disk, and do not trust third-party writeups of the format. Both go stale fast: between Codex 0.118 and 0.144 the shell-call mechanism, output shapes, and record vocabulary all changed; Gemini CLI ceased to exist as a product mid-2026; and community documentation of Antigravity's storage was wrong about the one thing that mattered (whether full transcripts are readable post-hoc).

Run the harness headlessly, today, and script the probes to exercise each tool family separately so the transcripts isolate the shapes:

- plain Q&A (no tools)
- shell/execute
- file write/edit
- file search (glob + grep over a seeded workdir), when the harness has dedicated search tools rather than searching via the shell
- URL fetch or web search
- a "do two things" prompt (to probe parallel tool calls)

Watch for CLI flag gotchas that corrupt probes (`agy --print` consumes the next argument as the prompt; auto-approve flags differ per harness). Record the exact harness version.

### Step 1: write the empirical inventory

Create `plans/<harness>-format-inventory.md` before writing any code, by scanning real transcripts (a Python one-off over the JSONL is fine). The existing three inventories are the templates. Answer at minimum:

- **File layout**: where transcripts live, naming, one file per session or nested (Claude Code subagents live under `<project>/<session>/subagents/`).
- **Multiple artifacts?** If the harness writes more than one file or variant per session, diff them **by content, not by record count** (Antigravity's `transcript.jsonl` and `transcript_full.jsonl` have identical counts but the short one trims fetched content). Pick the most complete and document the choice.
- **Record vocabulary**: every top-level type, key sets per type with presence ratios, and which types carry model-visible content vs. harness/UI bookkeeping.
- **Ordering guarantees**: is file line order event order? (Antigravity appends checkpoints out of step order; sort by `step_index`.) Are split messages contiguous? (Claude Code interleaves tool results mid-message.)
- **Message splitting/folding**: does one API message span multiple records? What identifies the message? Verify fold boundaries empirically before assuming.
- **Usage accounting**: where token usage lives and its semantics. Claude Code writes a growing snapshot per split record (take the last); Codex emits per-request `token_count` events (pool them and verify the sum property against the cumulative total); Antigravity has none.
- **Correlation**: native tool-call IDs, or positional pairing requiring synthesized IDs? Verify the pairing rule against tool-heavy transcripts.
- **Duplicates**: records that echo other records byte-for-byte (Codex `event_msg` user/agent messages) are skip-list candidates; verify the duplication claim.
- **Identity**: where session ID, harness version, model, cwd, and git branch live. Check field *types*, not just names (Antigravity's `error_code` is a number).
- **Origin**: how harness-injected content is distinguishable from human input (flags, roles, wrapper tags). Document heuristics and their failure modes.
- **Redacted/encrypted content**: thinking may be absent (Claude Code persists empty text plus signature), encrypted (Codex), or partial (Antigravity). Verify exhaustively before claiming absence: check for alternate keys, alternate record types, and sibling files.
- **Error shapes**: API errors, tool errors, retries.

Flag everything not observed locally as unverified. The loud-failure default covers the gaps; the doc keeps you honest about them.

### Step 2: decide the mapping

Write the native-to-event mapping table in the inventory doc before coding:

- Model-visible conversation records become events (`user_message`, `assistant_message`, `thinking`, `tool_call`, `tool_result`).
- Harness telemetry that is preserved-but-not-conversation becomes `system` events with a namespaced subtype and the payload in `Details`.
- Pure bookkeeping with no model-visible content goes on an explicit, enumerated **skip list**, counted via `OnSkip`. Byte-duplicate echoes of other records also qualify.
- Decide the loudness boundary for unknown subtypes: unknown record *types* and unknown conversation shapes stay loud; unknown telemetry subtypes may map to `system` events when the payload is preserved in full and the vocabulary churns per release (the Codex `event_msg` precedent).
- Exactly one `assistant_message` per API message (the accounting anchor), even if all its content became thinking or tool calls. Decide what closes the anchor and what usage attaches to it.
- Map native tool names onto ACP `ToolKind`s conservatively; unmappable names get `other`.
- Promote harness sidecar data: full sidecar rides verbatim in `tool_result.enrichment`; retrieval metrics (URL, raw bytes, status, duration) promote to `fetch` when present.

### Step 3: implement

Create `harness/<name>/` with three files, mirroring the existing adapters:

- `<name>.go`: `Adapter` struct (zero value usable, `var _ harness.Adapter = Adapter{}`), `ID()`, `Sniff()`, and the vocabulary tables (record types, skip list, tool-kind map).
- `parser.go`: `Events()` and the parser.
- `locate.go`: the `harness.Locator` discovery side — `DefaultRoot()`, `Scan()`, `Locate()` — encoding the harness's on-disk layout (where transcripts live, what identity is available from filenames/directory names vs. in-band). Identity reads go through `harness.ReadIdentity`, i.e. the adapter's own `Events` stream, never a second header parser. Scan accounts for every file under the root (ref, skip with a stable reason, or `*harness.ScanError` that does not end the scan); document the layout and any layout-derived identity enrichment in the file comment.

Conventions the existing adapters share (use `internal/parseutil` rather than re-implementing):

- Read lines through `parseutil.NewLineScanner`: it owns the first-line BOM strip, whitespace/CRLF trimming, blank-line skipping, line numbering, and the 256MB line cap (`parseutil.MaxLineBytes`) with its 64KB initial buffer.
- Embed `parseutil.Emitter` in the parser: it owns event emission, `*harness.ParseError` construction (`Fail`), permissive-mode unknown events (`Unknown`/`UnknownOrFail`), single-line provenance (`Prov`), and truncation (`Truncate`). Keep its `Line` (and `Version`, once discovered) current while scanning.
- Clone bytes (`parseutil.CloneRaw`) before retaining them; the scanner reuses its buffer.
- `parseutil.ParseTime` for timestamps (nil on absence or malformation; timestamps never fail a parse).
- Locators build refs with `harness.BuildRef`, report exclusions with `ScanOptions.Skip`/`SkipTree`, and resolve `Locate` globs with `harness.ResolveGlob` + `harness.ConfirmSessionID`.
- Provenance on every event: 1-based line, `EndLine` for folded ranges, raw records when `KeepRaw`.

`Sniff` rules: inspect only the first line (plus a marker-based fallback for headers cut off mid-line, since first lines can exceed the sniff window). Return `Certain` only on markers distinctive to this format, `Possible` for plausible-but-generic, `NoMatch` otherwise. It must return `NoMatch` for the other harnesses' transcripts; there are cross-harness Sniff tests to copy.

### Step 4: fixture

Write a **synthetic** fixture in `harness/<name>/testdata/`, modeled block-for-block on the real shapes. Synthetic because real transcripts embed vendor system prompts (license risk) and personal data (privacy). It must exercise every mapped record type plus every structural edge case the inventory found: the out-of-order record, the interleaved fold, the orphan result, the error record with its real field types.

### Step 5: tests

Copy the standard suite from an existing adapter and adapt:

- event-kind sequence against the fixture
- meta fields, usage totals, report contents
- fold/anchor behavior and tool pairing (including orphans and unanswered calls)
- user-message origins
- **line accounting**: every fixture line covered by an event's provenance range or a skip callback
- strict vs. permissive handling of unknown record types; malformed JSON with correct line numbers
- `Sniff`: own fixture (full and truncated header), other harnesses' lines, garbage
- `KeepRaw` and `MaxPayloadBytes`
- CRLF+BOM tolerance if the parser code path differs from the shared helpers
- an env-gated `TestLocalTranscripts` (pattern: `AGENTMINUTES_LOCAL_<NAME>_TRANSCRIPTS`) that strict-parses every real transcript on the developer's machine **with per-line accounting**. This is the test that catches what the fixture can't; it found a field-type mismatch within minutes on the Antigravity adapter.
- locator tests over a synthetic layout tree built in a temp dir (controlled mtimes): refs and their identity, skip reasons, malformed-transcript `ScanError` continuation, Since/Until behavior, `Locate` hit/miss/in-band-mismatch — plus an env-gated `TestLocalScan` calling `locatetest.Invariant`, the per-line accounting idea lifted to per-file.

### Step 6: register

All lists are alphabetical:

- `harness.ID` constant in `harness/harness.go`
- `harness.LastValidated` entry in `harness/versions.go` (the release the inventory was validated against; a registry test enforces presence)
- the facade registries in `agentminutes.go` — `adapters` and `locators` (explicit lists; no `init()` registration; a registry test keeps them in step)
- drift devtool wiring in `internal/driftprobe/`: a `vocabConfigs` entry (discriminator paths where the format hides its churn) and a generated `baselines/<id>.json`. Headless invocation comes from the sibling agentsummons library (`DefaultRunners` builds one runner per registered locator), so the new harness must be supported there first — its spec table is the single home of flag knowledge
- a `Detect` assertion in `agentminutes_test.go` proving disambiguation
- `--harness` flag help in `cmd/agentminutes/convert.go`, `sessions.go`, and `stats.go`
- README: support table row, the validated-versions sentence, caveats stated plainly
- root `doc.go` and the CLI long description if the harness list appears there

### Step 7: verify

```bash
go test ./... -count=1
golangci-lint run
GOOS=windows go build ./... && GOOS=windows go vet ./...
AGENTMINUTES_LOCAL_<NAME>_TRANSCRIPTS=<dir> go test ./harness/<name>/ -run 'TestLocal' -v
```

Then end-to-end: build the CLI and run `detect`, `convert` (both formats), `stats`, and `sessions --harness <name>` against a real transcript root. Auto-detection must pick the right adapter with the others registered.

## Harness format versioning

All three harness formats drift fast: Codex changed its shell-call mechanism, output shapes, and record vocabulary between 0.118 and 0.144; Antigravity churns per point release; Claude Code adds record types and envelope fields across 2.1.x. Decision, recorded here so it isn't relitigated per adapter: **parse by shape, not by version**. There is no per-version parser, and no user-facing version flag that changes parse behavior.

Why version-dispatched parsing is wrong for this domain:

1. **The version signal is unreliable or absent.** Claude Code stamps its version per record, Codex only in `session_meta`, Antigravity nowhere. A version flag would ask users for a fact the transcript doesn't record and they rarely know ("which Codex wrote this file in March?").
2. **Versions don't map to formats.** No harness documents or versions its transcript format; format changes are incidental to releases. A version-to-parser mapping would be empirically derived with unfillable holes (we observed Codex 0.118 and 0.144; the shapes of 0.119 through 0.143 are unknowable).
3. **One transcript can span versions.** A Claude Code session resumed after an upgrade contains records from multiple harness versions in one file, which is presumably why Claude Code stamps per record. Per-file version dispatch is wrong on arrival.
4. **Shape dispatch already works.** The Codex parser handles 0.118 and 0.144 in one code path by branching on which fields are present. Feature-detect, don't UA-sniff.

What we do instead:

- **Union parsers.** Each adapter accepts the union of all observed format variants, distinguishing by structure. Supporting a new harness release means widening the union, never forking a code path keyed on a version string.
- **Version-stamped fixtures are the multi-version support mechanism.** When a format changes, add fixtures for the new shape and keep the old ones; the tests then guarantee the union parser never regresses on formats we once handled. Note the observed version alongside each fixture shape.
- **Loud failure is the version-mismatch UX.** Unknown records produce a `*harness.ParseError` carrying `HarnessVersion` when the transcript self-records it. That error is the "unsupported version" signal, and it fires on the actual incompatibility rather than a version-number guess.
- **Coverage is documented as an observed range, not a compatibility promise.** README language is "validated against Claude Code 2.1.x transcripts through 2.1.204": we support shapes we've seen, and version numbers are just where we saw them. A "supported versions" table would imply we tested every release in the range, which is structurally impossible.
- **The two version axes stay independent.** `Meta.HarnessVersion` is input provenance, recorded for consumers to segment on. `SchemaVersion` is the output contract; harness format drift is absorbed by adapters and never bumps it.

The two places a version input is legitimate:

- **Metadata pass-through for formats that don't self-identify.** Antigravity records its version nowhere, so a caller-supplied version is fine: `Options.HarnessVersionHint` (CLI: `--harness-version`) is stamped into `Meta.HarnessVersion` and parse errors, never consulted for dispatch, and loses to any version the transcript itself records.
- **A genuinely ambiguous break.** If a harness ever reuses a record type or field with different semantics and no distinguishing marker, shape dispatch can't disambiguate. Branch on the transcript's self-declared version first (the Codex parser already retains it); fall back to a caller hint only if the format doesn't self-identify. Don't build this speculatively; all drift observed so far has been additive or structurally distinguishable.

The coverage record is machine-readable: `harness.LastValidated(id)` (a read-only accessor over the table in `harness/versions.go`) returns the newest release each harness's inventory was validated against, and `ParseError.Error` appends a drift hint when a failing transcript self-declares a newer version ("harness version 2.2.3, newer than last validated 2.1.197; likely format drift, check for an agentminutes update"). Update the table entry whenever an inventory is re-validated against a newer release; a registry test fails if an adapter lacks an entry. See also the format-drift infra notes in `plans/next-steps.md`.

## The drift devtool

`agentminutes drift` (implemented in `internal/driftprobe/`; design record in `plans/drift-probe-design.md`) is the on-demand drift checker. Three verbs; only `scan` appears in help, the other two are cobra-Hidden maintainer tooling (the Tailscale `debug` pattern):

- `drift scan <transcript>...` — free, user-facing: strict parse with line accounting plus a vocabulary diff (record types, depth-2 key sets, churn-prone discriminator values like Codex's `payload.type` and Claude Code's content-block types and tool names) against the embedded baseline. Point users here when they report unexpected parser output.
- `drift probe [--harness ...] [--force] [--keep]` — maintainer-only; spends tokens. Gates on installed version vs `LastValidated` (equal → skip unless `--force`), then invokes each harness headlessly (permission-skipping flags, disposable workdir) against six fixed probes (qa, shell, file, search, fetch, multi; search is claude-code-only, over a seeded workdir, because the other harnesses search via the shell). Each probe asserts its expected shapes actually appear in the parsed events; a missing shape retries once, then reports **inconclusive, not drift** — green always means the shapes were exercised. Exit codes: 0 clean, 1 drift, 2 inconclusive, 3 execution error.
- `drift baseline --harness <id> [--version <v>] -o <file> <paths>...` — regenerates a baseline JSON from transcripts. A directory the harness's locator recognizes as a transcript root contributes exactly the transcripts the locator discovers (sidecars like Antigravity's trimmed `transcript.jsonl` are excluded); any other directory is walked flat for `.jsonl`; non-matching files are skipped by sniff.

Baselines live embedded in `internal/driftprobe/baselines/<id>.json` and record **validated observed vocabulary** (generated from the fixtures plus the local real-transcript corpus). Two deliberate consequences: shapes implemented from documentation but never observed (e.g. Codex `function_call`) are absent, so their appearance in the wild flags for re-validation — a true positive, since those code paths have never been tested against reality; and MCP tool names are collapsed to `mcp__*` because they are user config, not harness vocabulary.

Reconciling drift: regenerate the affected baseline (`drift baseline` over fixtures + local corpus), bump `harness.LastValidated`, update the format inventory in `plans/`, and extend fixtures if shapes changed. `TestEmbeddedBaselines` fails if a baseline's `generated_from_version` disagrees with `LastValidated`, and `TestScanFixturesClean` pins that the committed fixtures scan clean against the committed baselines.

## Event transforms (post-parse policy)

Adapters translate with total accounting; they never make representational judgment calls. When a caller needs one — the canonical case is telemetry promotion, where harness telemetry is the *only* record of an action (Codex 0.144+ logs URL fetches solely as `event_msg` `web_search_end`) — it is a `session.Transform`: a pure function over the unified event stream, composed explicitly at the call site (`harness.Parse(a, r, opts, codex.PromoteWebSearch)`) or wrapped around `Adapter.Events`. Design record: `plans/telemetry-promotion.md`.

Rules for writing one: replace events rather than adding parallel representations; carry the source event's provenance so line accounting holds; mark synthesized tool events with `PromotedFrom` (the marker is the only runtime record that promotion happened, and it is what lets consumers dedupe against natively-recorded duplicates); pass errors through untouched; match only your own harness's vocabulary so the transform no-ops on other streams. Transforms live in the adapter package that owns the vocabulary, are exported and documented individually, and are never applied by default anywhere (facade registry, drift tool, CLI without `--promote`). The CLI's `--promote` table in `cmd/agentminutes/promote.go` is the one string boundary; add a row per new rule.

## Changing the schema

The JSON encoding of `session.Session` and `session.Event` is the cross-language contract (future npm/PyPI wrappers shell out to the CLI). Consequences:

- Every exported field needs a `schema:"acp|otel|ext"` provenance tag; `TestFieldProvenance` fails otherwise, and new payload structs must be reachable from its roots.
- Additive changes (new optional fields, new event kinds) still require updating `Event.Validate`, the README's schema section, and a `SchemaVersion` bump when the shape changes meaningfully.
- New event kinds also need a decision in `acp.Project`: do they project, or are they counted losses?
- Durations serialize as millisecond integers, not Go `time.Duration` nanoseconds.

## Windows

CI tests on windows-latest, and these rules keep it green: `.gitattributes` forces LF (a CRLF-mangled fixture fails only on Windows CI); all filesystem access via `filepath.Join`; never path-parse transcript *content* (Windows paths flow through as opaque strings); tolerate BOM and CRLF in inputs (shared helpers do this).

## Releases

Pushing a `v*` tag runs the release workflow: goreleaser (GitHub release, Homebrew tap), then npm and PyPI wrappers repackaging the release archives, versions stamped from the tag (see `wrappers/`). The `version` variable in `cmd/agentminutes/main.go` is the stamp point.

The GitHub release notes are the tag's CHANGELOG.md section: the workflow extracts it and fails the release before anything publishes if the section is missing. So promoting the Unreleased section of `CHANGELOG.md` to the new version heading is a required pre-tag step, not housekeeping.

Before tagging: agentsummons releases first (this repo depends on it for probe invocation — bump the `go.mod` dependency to its fresh tag), then run `drift probe` against any harnesses `harness.LastValidated` trails and reconcile per the drift-devtool section above, then promote the changelog.
