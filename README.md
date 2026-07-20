# agentminutes

Meeting minutes for your agents: parse native agent harness session logs into one unified, comparable event schema.

Agent harnesses record everything that happens in a session (messages, tool calls, tool results, token usage) in their native transcript files, and every harness invents its own format. Those transcripts are ground truth for how an agent actually behaved: which tools it chose, what it retrieved, what it saw back, what it spent. agentminutes parses them into a single event schema so you can analyze and compare sessions across harnesses, in Go or from the command line.

## Status

Early development, pre-release. The normalized schema is versioned (currently `0.1.0`) and appears in every output record; expect it to evolve until 1.0.

| Harness | Status | Native format |
| --- | --- | --- |
| Antigravity CLI | Supported | `~/.gemini/antigravity-cli/brain/<id>/.system_generated/logs/transcript_full.jsonl` |
| Claude Code | Supported | `~/.claude/projects/<project>/*.jsonl` |
| Codex CLI | Supported | `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` |
| Gemini CLI (classic) | Not planned | Retired for individual users June 2026; Antigravity is its successor |

Adapters are validated against real transcripts (Antigravity 1.1.1 through 1.1.4; Claude Code 2.1.153 through 2.1.205; Codex 0.118 and 0.144 through 0.144.6) with a mechanical line-accounting check: every source line becomes an event, a counted skip, or an error. See [Design notes](#design-notes) for why that matters. Antigravity and Codex coverage is thinner than Claude Code's (fewer local transcripts) and both formats drift quickly between releases; see the inventories under `plans/` for what is verified vs. implemented from documented shapes. Antigravity transcripts carry no token usage, and their tool calls have no correlation IDs (the adapter synthesizes step-derived IDs and pairs positionally).

## Install

CLI:

```bash
brew install agent-ecosystem/tap/agentminutes
# or
npm install -g agentminutes
# or
pip install agentminutes
# or
go install github.com/agent-ecosystem/agentminutes/cmd/agentminutes@latest
```

The npm and pip packages wrap the same prebuilt Go binary (macOS, Linux,
and Windows on x64/arm64); nothing extra is downloaded at install time.
Prebuilt static binaries are also on the
[releases page](https://github.com/agent-ecosystem/agentminutes/releases).

Library:

```bash
go get github.com/agent-ecosystem/agentminutes
```

## CLI usage

Convert a transcript to a normalized session record (harness auto-detected):

```bash
agentminutes convert ~/.claude/projects/my-project/some-session.jsonl
```

The default output is one JSON document: session metadata, the ordered events, token totals, and a report accounting for everything that did not become an event. `totals` is omitted entirely when the transcript records no usage (Antigravity), so a measured zero is never conflated with "not recorded".

Stream events as JSONL instead (one event per line on stdout; the same per-type skip accounting as the json report, summarized on stderr):

```bash
agentminutes convert --format jsonl session.jsonl | jq -r 'select(.kind == "tool_call") | .tool_call.name'
```

Find the transcripts you care about without parsing everything. Harnesses write to global, harness-owned locations (`~/.claude/projects`, `~/.codex/sessions`, `~/.gemini/antigravity-cli/brain`), so `sessions` scans those roots, reads each transcript's identity cheaply (a header read via the real parser, not a second format), and filters by the keys harnesses actually record:

```bash
# Every session that ran in a given working directory, ready to pipe:
agentminutes sessions --cwd ~/runs/exp-42 | xargs -n1 agentminutes convert

# One known session, resolved directly (Claude Code subagent transcripts
# are included in the output; forgetting them is the classic archiving bug):
agentminutes sessions --harness claude-code --session-id 0199c9a3-...

# Full metadata instead of paths, bounded by time:
agentminutes sessions --since 2026-07-19 --format jsonl
```

Filters: `--harness`, `--root` (explicit root, requires `--harness`), `--cwd`, `--session-id`, `--since`/`--until` (RFC 3339 or bare dates). A stderr summary accounts for what didn't match and why; sessions with no recorded cwd (Antigravity records none) never match `--cwd` and are counted rather than silently missing. Per-file scan errors go to stderr and set exit status 1 without aborting the scan.

Identify a transcript's harness:

```bash
$ agentminutes detect session.jsonl
claude-code	certain
```

Summarize a session's behavior (tool mix, bytes retrieved, latency, tokens, observed models, final answer). `models` lists the models observed on assistant messages in first-observed order, so more than one entry means the serving model changed mid-session:

```bash
agentminutes stats session.jsonl
```

The summary's `system_by_subtype` counts surface actions a harness records only as telemetry (Codex 0.144 logs URL fetches solely as `web_search_end` events, and 0.144.6 records file edits solely as `patch_apply_end`, so they appear there rather than as tool calls). To count them as tool calls, opt into the promotions — `--promote` works on `stats` as well as `convert`:

```bash
agentminutes stats --promote codex:web-search --promote codex:patch-apply rollout.jsonl
```

If a parse fails or the output looks wrong, check whether the transcript's format has drifted past what this build was validated against (free; nothing is invoked):

```bash
$ agentminutes drift scan session.jsonl
session.jsonl: claude-code
  drift: record type "assistant" has new key "responseMeta"
```

A drift finding usually means the harness updated its log format; check for an agentminutes update or file an issue with the scan output. Exit codes: 0 clean, 1 drift.

Useful flags for `convert`:

- `--harness claude-code` skips auto-detection (also on `stats`).
- `--harness-version 1.1.1` (also on `stats`) records the harness version for formats that don't record one themselves (Antigravity). Metadata only; it never changes how the transcript is parsed, and a version the transcript records wins.
- `--permissive` (also on `stats`) preserves unclassifiable records as `unknown` events instead of failing the parse.
- `--keep-raw` retains the verbatim native records in each event's provenance.
- `--max-payload-bytes N` replaces tool-result payloads larger than N with size-and-digest placeholders.
- `--promote codex:web-search` / `--promote codex:patch-apply` (also on `stats`) opt into a telemetry promotion: some harness versions record an action only as telemetry (Codex 0.144+ logs URL fetches solely as `web_search_end` events, and 0.144.6 records file edits solely as `patch_apply_end`), and promotion synthesizes the corresponding `tool_call`/`tool_result` pair so tool metrics see it. Never on by default because versions that also record the action natively would double-count; synthesized events carry `promoted_from` so they stay auditable.
- `-o out.json` (also on `stats`) writes to a file; passing `-` as the transcript reads stdin.

## Library usage

Whole-file parsing:

```go
f, err := os.Open("session.jsonl")
if err != nil {
    return err
}
defer f.Close()

s, err := agentminutes.Parse(f, harness.ClaudeCode, harness.Options{})
if err != nil {
    return err
}

fmt.Println(s.Meta.HarnessVersion, len(s.Events), s.Totals.OutputTokens)

// Join tool calls with their results, in call order.
for _, ti := range s.ToolInteractions() {
    if ti.Call == nil {
        continue // orphaned result: no matching call in the transcript
    }
    call := ti.Call.ToolCall
    fmt.Println(call.Name, call.Kind, len(ti.Results))
}

// Or take the precomputed behavioral summary.
st := s.Stats()
fmt.Println(st.ToolCallsByName, st.ResultBytes, st.WallTimeMS, st.FinalAnswer)
```

Project a session onto the ACP session/update vocabulary, with a loss report quantifying what the ACP lens cannot see (system events, token usage, timestamps, empty anchors):

```go
updates, loss := acp.Project(s)
fmt.Println(len(updates), loss.DroppedEvents, loss.DroppedFields)
```

Streaming, for large transcripts or incremental processing:

```go
a, err := agentminutes.AdapterFor(harness.ClaudeCode)
if err != nil {
    return err
}
for ev, err := range a.Events(f, harness.Options{}) {
    if err != nil {
        return err // a *harness.ParseError identifying harness, version, and line
    }
    if ev.Kind == session.KindToolCall {
        fmt.Println(ev.ToolCall.Name)
    }
}
```

The first event of every stream is `session_meta`, so streaming consumers know what they are reading before EOF. `session.Accumulator` bridges the two modes: feed it events, ask it for the accumulated `Session` (this is exactly what `Parse` does).

Optional post-parse policies compose as `session.Transform` functions, applied in order by `Parse` or wrapped around `Events` directly. Adapters translate; transforms reshape, and only when asked. The exported telemetry promotions (currently `codex.PromotePatchApply` and `codex.PromoteWebSearch`) are the canonical transforms:

```go
s, err := harness.Parse(codex.Adapter{}, f, harness.Options{}, codex.PromoteWebSearch)
```

Session discovery mirrors the CLI's `sessions` command. `agentminutes.Scan` enumerates every harness's default root; `agentminutes.Locate` resolves a known session ID to its transcript path(s) — the capture-time primitive for runners that archive transcripts right after a headless invocation:

```go
ref, err := agentminutes.Locate(harness.ClaudeCode, sessionID)
if err != nil {
    return err // wraps os.ErrNotExist when the session has no transcript
}
archive(ref.Path)                    // the main transcript
for _, p := range ref.SubagentPaths { // Claude Code agent-*.jsonl files
    archive(p)
}
```

Scans obey the same accounting discipline as parses, lifted from per-line to per-file: everything under a root is a yielded ref, a reported skip (`ScanOptions.OnSkip`), or a `*harness.ScanError` — which, unlike a parse error, does not end the scan. Discovery never parses beyond each transcript's head, and it never invents identity except where the layout is the documented source (an Antigravity session ID is its conversation directory name; the format records none in-band). For non-default roots, use `agentminutes.LocatorFor(id).Scan(root, opts)`.

## The schema

A normalized session is an ordered list of events. Exactly one payload field is set per event, matching its `kind`:

| Kind | Payload | ACP analog |
| --- | --- | --- |
| `session_meta` | Session identity: harness, version, session ID, cwd | none (extension) |
| `user_message` | User-role content, with an origin marker: `human` or `harness` | `user_message_chunk` |
| `assistant_message` | One complete assistant API message | `agent_message_chunk` |
| `thinking` | Extended-thinking block | `agent_thought_chunk` |
| `tool_call` | Tool invocation with full input | `tool_call` |
| `tool_result` | Tool outcome, correlated by tool call ID | `tool_call_update` |
| `system` | Harness/API activity: injected context, diagnostics, errors | none (extension) |
| `unknown` | Unclassifiable record, preserved verbatim (permissive mode only) | none (extension) |

Points worth knowing:

- **The event vocabulary is borrowed, not invented.** Event kinds and tool classifications follow the [Agent Client Protocol](https://agentclientprotocol.com/)'s session-update vocabulary where an analog exists; token usage fields follow OTel GenAI semantic conventions. Every field in the schema is tagged with its provenance (`acp`, `otel`, or `ext`), enforced by a test, so the mapping cannot rot.
- **`assistant_message` is the accounting anchor.** Harnesses may split one API message across many records with usage written as a growing snapshot; the adapter folds them and takes the final snapshot. Exactly one `assistant_message` is emitted per API message, even when all of its content became `thinking` or `tool_call` events, so token totals are always derivable. Events from the same API message share a `message_id`.
- **`tool_call` and `tool_result` stay separate, in stream order.** Ordering is data: interleaving, parallel tool execution, and retries are visible in the sequence. `Session.ToolInteractions()` provides the joined view, including unanswered calls and orphaned results.
- **Results carry what the model saw, plus what the harness knew.** `tool_result.content` is the post-pipeline content the model actually received. Harness sidecar data rides along verbatim in `enrichment`, with retrieval metrics promoted to `fetch` (URL, raw bytes fetched, status, duration) when present. For summarizing pipelines like Claude Code's WebFetch, comparing `fetch.raw_bytes` against the content size measures the pipeline's compression directly.
- **Every event points back at its source.** `provenance` carries the 1-based line range in the native transcript, and optionally the verbatim records (`--keep-raw`).
- **The report closes the loop.** `report` counts skipped record types (harness UI bookkeeping with no model-visible content), unknown events, and orphaned tool results. Between events, skips, and errors, every input line is accounted for.

The JSON encoding of `Session` and `Event` is the cross-language output contract; the `agentminutes_schema` field identifies its revision. The Go types in [`session`](session/) are documentation for it.

## Design notes

- **Loud failure.** An unrecognized record type or a malformed record is a parse error identifying the harness, harness version, and line, never a silent skip. When the transcript was written by a harness release newer than the last one validated (`harness.LastValidated`), the error says so: the likely cause is format drift, and the fix is usually an agentminutes update. Dropped events corrupt behavioral metrics; if you prefer degraded output over failure, opt in with `--permissive` and the dropped-nothing guarantee moves into `unknown` events. Adapters are tested with a line-accounting invariant that mechanically verifies the guarantee.
- **Parsing is post-hoc, not in-band.** agentminutes reads the transcripts harnesses already write. Driving harnesses through adapter protocols could perturb the behavior being measured, so ACP contributes its schema here, not its transport.
- **Streaming is the core.** Adapters emit `iter.Seq2[session.Event, error]`; whole-file parsing is a thin accumulator over it. Note that streaming does not guarantee constant memory for every harness: line-oriented formats (Claude Code) stream genuinely, while single-document formats must buffer internally.
- **Explicit registry.** Supported adapters are a visible list in the facade, not `init()` side effects.

## Development

```bash
go test ./...        # unit tests, fixture-based
golangci-lint run    # lint + gofumpt formatting

# Validate the Claude Code adapter against your own real transcripts
# (strict mode, with per-line accounting):
AGENTMINUTES_LOCAL_TRANSCRIPTS=~/.claude/projects go test ./harness/claudecode/ -run TestLocalTranscripts -v
```

Adapters live under [`harness/`](harness/), one package per harness, implementing the `harness.Adapter` interface. To add one, follow [DEVELOPMENT.md](DEVELOPMENT.md): it covers the full process from generating fresh ground-truth transcripts and writing a format inventory (see the existing ones under [`plans/`](plans/)) through implementation conventions, the standard test suite, registration, and verification.

## Roadmap

- Antigravity CLI adapter (evaluated viable; see `plans/antigravity-format-inventory.md`).
- `acp.Parse`: the projection's inverse, parsing recorded ACP session streams as a peer adapter.
- npm and PyPI wrapper packages bundling the CLI binary.

## License

[MIT](LICENSE)
