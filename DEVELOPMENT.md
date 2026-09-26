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

Copilot CLI (September 2026) is the reference change: one working tree touching every spot below, three probe rounds, six fixtures. The agentsummons side lands first (its `DEVELOPMENT.md` has the invocation-side checklist) and hands over a `plans/<harness>-handoff.md` with the headless flags, the transcript location, and the session ids of the probes it ran; those sessions are the first ground truth, and the inventory you write in step 1 replaces the handoff note.

### Step 0: generate fresh ground truth

Do not trust transcripts already on disk, and do not trust third-party writeups of the format. Both go stale fast: between Codex 0.118 and 0.144 the shell-call mechanism, output shapes, and record vocabulary all changed; Gemini CLI ceased to exist as a product mid-2026; and community documentation of Antigravity's storage was wrong about the one thing that mattered (whether full transcripts are readable post-hoc).

Run the harness headlessly, today, and script the probes to exercise each tool family separately so the transcripts isolate the shapes. One deterministic action per prompt, naming the tool ("use the view tool to read note.txt, then reply with exactly: done"), seeded files where a tool needs something to act on, one scratch directory per probe (the drift probe's foreign-session veto keys on the recorded cwd), runs in sequence rather than in parallel (a shared session store), and stdin closed. Each probe costs about one premium request; the whole list below cost Copilot roughly forty, less than one wrong mapping.

The families, in the order they tend to change the adapter:

- plain Q&A (no tools)
- shell/execute, plus a command that exits nonzero
- file read, write, and edit, plus a read of an absent path and an edit whose target text is missing
- file search (glob + grep over a seeded workdir), when the harness has dedicated search tools rather than searching via the shell
- URL fetch or web search, plus a fetch that 404s
- a "do two things" prompt (parallel tool calls)
- the same write with no approval bypass (the denial shape)
- a resume of an earlier session, and a resume that switches models
- one model per API the harness speaks. Reasoning is recorded per API, not per vendor: on Copilot, six model families reduce to three shapes because GPT, Grok, and MAI all arrive through the OpenAI Responses shape. Read the vendor's supported-models page for the list, and expect the model id to be the documented name lowercased. A rejected model id can leave a session directory with no transcript, which is itself a locator case worth keeping.
- subagents in every mode the harness offers: sync, two in one message, background with whatever follow-up tools read and steer it, nested (a subagent that delegates), a dedicated search agent, and a user-defined agent
- a binary result (view an image), for the asset record and the content-block shape
- anything the harness loads as context on demand (skills, instructions) and any MCP path, both built-in and user-configured

Two free sources before spending a request: the transcript itself usually names the registered tools (Copilot lists them in every usage checkpoint), and a recorded system prompt embeds each tool's usage instructions, including argument names and modes such as background delegation. Read those locally; never commit them.

Watch for CLI flag gotchas that corrupt probes (`agy --print` consumes the next argument as the prompt; auto-approve flags differ per harness; macOS has no `timeout`). Record the exact harness version. Expect more than one round: the first pass shows which families exist, the transcripts of the second pass show what you still have not seen (unobserved keys and types come out of `drift scan` against a provisional baseline), and the third closes the gaps.

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
- **Redacted/encrypted content**: thinking may be absent (Claude Code persists empty text plus signature), encrypted (Codex), or partial (Antigravity), and it may differ per model API within one harness (Copilot: Anthropic thinking blocks, OpenAI Responses encrypted blocks, and a flattened text-plus-opaque pair for the rest). Verify exhaustively before claiming absence: check for alternate keys, alternate record types, and sibling files.
- **Subagents**: separate files (Claude Code, Codex), sibling conversations (Antigravity), or interleaved in the parent transcript (Copilot, with an envelope agent id and lifecycle records). For the interleaved case, record how a subagent's prompt is marked as agent-authored, how nesting is linked, and whether follow-up turns get a new lifecycle bracket.
- **Error shapes**: API errors, tool errors, retries. Tool errors have at least two shapes to tell apart: the tool could not run (a denial, a missing path) versus the tool ran and reported failure (a nonzero exit, a 404), which some harnesses record as success.
- **Binary results**: how an image the model saw is stored (Copilot: a separate asset record carrying the bytes, referenced from the result by id).

Flag everything not observed locally as unverified. The loud-failure default covers the gaps; the doc keeps you honest about them.

### Step 2: decide the mapping

Write the native-to-event mapping table in the inventory doc before coding:

- Model-visible conversation records become events (`user_message`, `assistant_message`, `thinking`, `tool_call`, `tool_result`).
- Harness telemetry that is preserved-but-not-conversation becomes `system` events with a namespaced subtype and the payload in `Details`.
- **A `system` event's `Text` is the record's model-visible text, when the record carries one** (design record: `plans/model-visible-text.md`). Downstream consumers (skillxp's trace package is the first) locate a phrase across a session by reading `user_message` content, `tool_result` content, and `system.Text`; they never parse `Details`, whose shape is per harness and per release. A record whose text lives only in `Details` is therefore invisible to them, and the inventory's "carries model-visible content" column is the checklist for which records need a `Text` case: the system prompt, injected instructions and reminders, a delivered skill body, a harness-authored prompt. Fill `Text` with the record's primary string, bare (no wrapper the harness adds on delivery; that stays in `Details`), and do not fill it for bytes, ids, paths, or structured listings that have no single text. This is translation, not a judgment call: it asserts nothing about the role the model received the text in. When the transcript also records the delivered form (Claude Code's rendered `<system-reminder>` blocks, Copilot's `<skill-context>` wrapper named by the delivery record), `Options.TextForm = TextDelivered` selects it; the adapter implements both forms, and a record with no recorded delivered form keeps its bare text in either. When the role matters and the transcript records it (Claude Code's `isMeta` user records), the record is a `user_message` with origin `harness` instead; when it is not recorded (Copilot's `skill.invoked`, whose delivery record hashes the body but names no role), the event stays `system` with the body as `Text`, and a conversation-shaped synthesis is a transform's job.
- Pure bookkeeping with no model-visible content goes on an explicit, enumerated **skip list**, counted via `OnSkip`. Byte-duplicate echoes of other records also qualify.
- Decide the loudness boundary for unknown subtypes: unknown record *types* and unknown conversation shapes stay loud; unknown telemetry subtypes may map to `system` events when the payload is preserved in full and the vocabulary churns per release (the Codex `event_msg` precedent).
- Exactly one `assistant_message` per API message (the accounting anchor), even if all its content became thinking or tool calls. Decide what closes the anchor and what usage attaches to it.
- Map native tool names onto ACP `ToolKind`s conservatively; unmappable names get `other`. A tool that reads a background agent's replies is `read` (the Claude Code `TaskOutput` precedent); delegation tools stay `other`.
- Promote harness sidecar data: full sidecar rides verbatim in `tool_result.enrichment`; retrieval metrics (URL, raw bytes, status, duration) promote to `fetch` when present, on failed fetches too. Do not promote a size that is measured after conversion; leave `raw_bytes` unset rather than report the wrong quantity.
- A failed call is `is_error` whether the harness says the tool could not run or the tool ran and failed (a nonzero shell exit is an error on every adapter). When a failure has no result content, feed the error message to the result so the model-visible outcome is not empty.
- A binary result becomes an `image` (or `other`) content block holding the harness's descriptor, with the bytes left in the asset record's `system` event; never inline base64 into the block.

Precedents for decisions the schema does not make for you:

- **Namespaced record types.** When a format's types are namespaced (`session.*`, `permission.*`), the loudness boundary can follow the namespace: unknown types under telemetry namespaces map to `system` events with the payload in full, unknown types under conversation namespaces (`assistant.*`, `user.*`, `tool.*`) fail loudly. Every namespace still needs a known-types table so the baseline sees the vocabulary.
- **Record type decides kind, authorship rides in Details.** A human-typed prompt the harness delivers as injected context (Claude Code's `queued_command` attachment with `commandMode: prompt` and `origin.kind: human`, typed while the model worked and never re-recorded as a `user` record) is a `system` event with the prompt as `Text`, not a `user_message`: the harness itself chose to deliver it as a reminder, and user-message counts follow the harness's turn structure. The origin marker exists for the opposite case (harness text inside a user record). Consumers counting user intent read the authorship from `Details`.
- **Interleaved subagents.** A subagent whose records share the parent's file is attributed with `Event.AgentID` (stamped on every one of its events), its delegated prompt is `user_message` with origin `harness` (the parent agent wrote it), and its lifecycle records are `system` events; `Meta.IsSubagent` stays for harnesses that write separate files. This was the one schema addition Copilot needed, additive and with no `SchemaVersion` bump. Either way the task join is the locator's `Gather`, and `agentminutes.Task` builds the task summary on top of it; the subagent drift probe's `Check` pins that join and the sum property on fresh delegations, so a harness moving its subagents is loud there.
- **Usage that is not per message.** When the format records only session-cumulative totals (Copilot's shutdown record) or a prompt-side snapshot, attach nothing to assistant messages and leave `totals` nil: the per-message `Usage` field must not carry a session total. Preserve the records as `system` events, document the field semantics in the inventory (which number is the whole prompt, which is the uncached remainder), and add the harness's row to the docs site's usage-source table.

### Step 3: implement

Create `harness/<name>/` with three files, mirroring the existing adapters:

- `<name>.go`: `Adapter` struct (zero value usable, `var _ harness.Adapter = Adapter{}`), `ID()`, `Sniff()`, and the vocabulary tables (record types, skip list, tool-kind map).
- `parser.go`: `Events()` and the parser.
- `locate.go`: the `harness.Locator` discovery side — `DefaultRoot()`, `Scan()`, `Locate()`, and `Gather()` (the task join: how the harness's subagent transcripts are found from a parent ref, named by a `harness.Join*` value; a harness that records subagents inline returns the parent alone) — encoding the harness's on-disk layout (where transcripts live, what identity is available from filenames/directory names vs. in-band). Identity reads go through `harness.ReadIdentity`, i.e. the adapter's own `Events` stream, never a second header parser. Scan accounts for every file under the root (ref, skip with a stable reason, or `*harness.ScanError` that does not end the scan); document the layout and any layout-derived identity enrichment in the file comment.

Conventions the existing adapters share (use `internal/parseutil` rather than re-implementing):

- Read lines through `parseutil.NewLineScanner`: it owns the first-line BOM strip, whitespace/CRLF trimming, blank-line skipping, line numbering, and the 256MB line cap (`parseutil.MaxLineBytes`) with its 64KB initial buffer.
- Embed `parseutil.Emitter` in the parser: it owns event emission, `*harness.ParseError` construction (`Fail`), permissive-mode unknown events (`Unknown`/`UnknownOrFail`), single-line provenance (`Prov`), and truncation (`Truncate`). Keep its `Line` (and `Version`, once discovered) current while scanning.
- Clone bytes (`parseutil.CloneRaw`) before retaining them; the scanner reuses its buffer.
- `parseutil.ParseTime` for timestamps (nil on absence or malformation; timestamps never fail a parse).
- Locators build refs with `harness.BuildRef`, report exclusions with `ScanOptions.Skip`/`SkipTree`, and resolve `Locate` globs with `harness.ResolveGlob` + `harness.ConfirmSessionID`.
- Provenance on every event: 1-based line, `EndLine` for folded ranges, raw records when `KeepRaw`.

`Sniff` rules: inspect only the first line (plus a marker-based fallback for headers cut off mid-line, since first lines can exceed the sniff window). Return `Certain` only on markers distinctive to this format, `Possible` for plausible-but-generic, `NoMatch` otherwise. It must return `NoMatch` for the other harnesses' transcripts; there are cross-harness Sniff tests to copy.

### Step 4: fixtures

Write **synthetic** fixtures in `harness/<name>/testdata/`, modeled block-for-block on the real shapes. Synthetic because real transcripts embed vendor system prompts (license risk) and personal data (privacy). Together they must exercise every mapped record type plus every structural edge case the inventory found: the out-of-order record, the interleaved fold, the orphan result, the error record with its real field types.

Generate them rather than hand-write them: a throwaway script with one helper per record type (Copilot's lived in the session scratchpad) produces valid, compact JSONL with correct chain ids and monotonic timestamps, and makes a 145-line interleaved-subagent fixture a few minutes' work. Commit the output only. Split by shape family (base conversation, subagents, tools, errors, ...) instead of growing one fixture: an event-sequence test over a single file breaks every time a later round adds a shape, while a new fixture with its own test leaves the old ones untouched. When a test asserts a count, derive it from the fixture spec rather than from memory; two of Copilot's first-run failures were miscounted expectations.

### Step 5: tests

Copy the standard suite from an existing adapter and adapt:

- event-kind sequence against the fixture
- meta fields, usage totals, report contents
- fold/anchor behavior and tool pairing (including orphans and unanswered calls)
- user-message origins
- **line accounting**: every fixture line covered by an event's provenance range or a skip callback
- **text audit**: `textaudit.Invariant` over every fixture in both text forms, with the adapter's `nonText` map (a `(subtype, path)` key per long string that is deliberately not surfaced, each with its reason: a path, bytes, tool schemas, the call's own input, a UI rendering the model did not receive, an echo of text surfaced elsewhere, promotable telemetry). Two rules: an event that surfaces no text may carry no long string; an event that surfaces text may carry no long string that neither contains nor is contained by it (a second text). Tool results are held to the same rules on their content against their enrichment, which is where the "which field did the model see" choice is pinned. The same map runs in the local corpus test below, which is where new attachment types and moved keys show up first
- **delivery markers**: where the harness records that it delivered text, a threshold-free check on that record (Claude Code: a rendered attachment surfaces text; Copilot: a `skill.context_delivered_ref` hash matches a surfaced skill body). These catch the one-line reminders the length rule cannot
- strict vs. permissive handling of unknown record types; malformed JSON with correct line numbers
- `Sniff`: own fixture (full and truncated header), other harnesses' lines, garbage
- `KeepRaw` and `MaxPayloadBytes`
- CRLF+BOM tolerance if the parser code path differs from the shared helpers
- an env-gated `TestLocalTranscripts` (pattern: `AGENTMINUTES_LOCAL_<NAME>_TRANSCRIPTS`) that strict-parses every real transcript on the developer's machine **with per-line accounting**. This is the test that catches what the fixture can't; it found a field-type mismatch within minutes on the Antigravity adapter.
- locator tests over a synthetic layout tree built in a temp dir (controlled mtimes): refs and their identity, skip reasons, malformed-transcript `ScanError` continuation, Since/Until behavior, `Locate` hit/miss/in-band-mismatch, and `Gather` over a parent with subagents (nested where the harness allows it) — plus env-gated `TestLocalScan` and `TestLocalTask` calling `locatetest.Invariant` and `locatetest.TaskInvariant`: the per-line accounting idea lifted to per-file, and the task join checked as a partition (every subagent transcript discovery finds is claimed by exactly one task; a subagent whose parent is gone from the store is logged as an orphan, not failed).

### Step 6: register

All lists are alphabetical:

- `harness.ID` constant in `harness/harness.go`
- `harness.LastValidated` entry in `harness/versions.go` (the release the inventory was validated against; a registry test enforces presence)
- the facade registries in `agentminutes.go` — `adapters` and `locators` (explicit lists; no `init()` registration; a registry test keeps them in step)
- drift devtool wiring in `internal/driftprobe/`: a `vocabConfigs` entry (discriminator paths where the format hides its churn, with a `normalize` for user-configured vocabulary such as MCP tool names when a prefix identifies it), a generated `baselines/<id>.json`, every fixture in `TestScanFixturesClean`, the runner list in `TestDefaultRunnersAlphabetical`, and a harness-scoped search probe in `DefaultProbes` when the harness has dedicated search tools (name-exact, with the tool restriction that keeps the shell out of reach). Headless invocation comes from the sibling agentsummons library (`DefaultRunners` builds one runner per registered locator), so the new harness must be supported there first — its spec table is the single home of flag knowledge
- a `Detect` assertion in `agentminutes_test.go` proving disambiguation
- `--harness` flag help derives from the registry; the CLI long description in `cmd/agentminutes/main.go`, root `doc.go`, `.goreleaser.yaml`, and `CLAUDE.md` (the description and the local-validation command block) spell the list out
- README: support table row (alphabetical, so a "not planned" row can sit between supported ones), caveats stated plainly
- the docs site, none of it enforced (grep the whole tree for the previous harness's name and for "three"/"four", excluding `plans/` and the changelog): `hugo.toml` (the site description), `content/_index.md`, `content/docs/_index.md`, and `data/landing.yaml` descriptions, `docs/harnesses.md` (table row and a caveat paragraph), `docs/discovery.md` (the roots list), `docs/use-cases.md`, `docs/token-comparison.md` (a row in the usage-source table, and a bullet if the field semantics are new), `docs/schema.md` (any new field or exception, such as inline subagents), `docs/subagents.md` (a section per harness), and `sharing-image.html` (the tagline names every harness), followed by `site/generate_sharing_image` to re-render `static/sharing.png` with headless Chrome
- the wrappers: `wrappers/npm/package.json` and `wrappers/pypi/pyproject.toml` keywords, both wrapper READMEs, and the PyPI `__init__.py` docstring (the wrapper tests are harness-agnostic, so nothing fails when these are stale)
- `CHANGELOG.md` Unreleased, and `plans/next-steps.md` for what stayed unobserved

### Step 7: verify

```bash
go test ./... -count=1
golangci-lint run
GOOS=windows go build ./... && GOOS=windows go vet ./...
AGENTMINUTES_LOCAL_<NAME>_TRANSCRIPTS=<dir> go test ./harness/<name>/ -run 'TestLocal' -v
```

Then end-to-end: build the CLI and run `detect`, `convert` (both formats), `stats`, and `sessions --harness <name>` against a real transcript root. Auto-detection must pick the right adapter with the others registered.

The local corpus test also runs the text audit (`internal/textaudit`) and the delivery-marker checks: every `system` event or tool result whose details or enrichment carry a string of 80+ bytes that its text does not account for fails unless its `(subtype, path)` is on the adapter's `nonText` map. Each failure is a decision: surface the string (it is model-visible text the event misses, or the adapter read the wrong field) or add the key with its reason (a path, bytes, tool schemas, the call's input, a UI rendering, an echo of text surfaced by another event, promotable telemetry). Never widen the map to make the test pass without writing the reason down; when the reason is "the transcript does not say", write that, and record the open question in the inventory.

Then the drift loop, once per probe round: `drift scan` over the local corpus names every key, type, and discriminator value the baseline has not seen (tool names, error codes, provider ids), which is the list of what to inventory or widen next; regenerate the baseline over the fixtures plus the corpus root when the round is reconciled (`TestScanFixturesClean` pins the fixtures against it, so an unregenerated baseline fails the suite). Finally `drift probe --harness <id> --force --keep`, which exercises the eight standard probes end to end and is the check that the search and subagent probes' tool names are right.

Docs pages go through `site/check_prose_style`, `vale --config site/.vale.ini README.md`, and a `hugo` build before they are done.

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
- `drift probe [--harness ...] [--force] [--keep]` — maintainer-only; spends tokens. Gates on installed version vs `LastValidated` (equal → skip unless `--force`), then invokes each harness headlessly (permission-skipping flags, disposable workdir) against eight fixed probes (qa, shell, file, search, fetch, multi, failure, subagent; search runs on the harnesses with dedicated search tools, claude-code and copilot, over a seeded workdir, because the others search via the shell; subagent is name-exact per harness for each one's delegation tool). Each probe asserts its expected shapes actually appear in the parsed events; a missing shape retries once, then reports **inconclusive, not drift** — green always means the shapes were exercised. Exit codes: 0 clean, 1 drift, 2 inconclusive, 3 execution error.
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
