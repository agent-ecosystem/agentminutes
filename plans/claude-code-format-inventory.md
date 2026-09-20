# Claude Code transcript format: empirical inventory

Status: findings
Source: exhaustive scan of 33 local JSONL transcripts (4,220 records, 0 parse errors) under `~/.claude/projects/`, harness versions 2.1.153, 2.1.177, 2.1.187, 2.1.197. Re-validated on 2.1.204 by a clean drift probe (all six probes exercised and parsed; vocabulary unchanged against the baseline). Re-validated on 2.1.205 by baseline reconciliation over the local corpus, triggered by `drift scan` flagging tool-denial transcripts: five additive keys absorbed (`toolDenialKind`, `session_id`, `pendingBackgroundAgentCount`, and the `dangerouslyDisableSandbox`/`staleRecovered` sidecar keys; each documented in its section below), no structural changes. Re-validated on 2.1.212 by drift probe (all six probes exercised and parsed): one new record type, `file-history-delta` (skip-listed; see the skip table), plus additive keys `effort` (assistant envelope), `gitOperation`/`backgroundCwdHint` (Bash sidecar), and `totalFiles` (Grep sidecar); the same reconciliation absorbed `imagePasteIds`/`interruptedMessageId` (user envelope, 2.1.205-era interactive sessions) from the local corpus. Re-validated on 2.1.231 by drift probe (all six probes exercised and parsed; probe vocabulary unchanged); the local-corpus reconciliation that followed surfaced one new record type, `pr-link` (observed from 2.1.220: session-to-PR linkage written when a session creates or attaches to a GitHub PR — keys `sessionId`, `prNumber`, `prUrl`, `prRepository`, `timestamp`; no model-visible content, skip-listed), plus additive corpus keys (`attachment.hookEvent`/`hookName`/`imagePasteIds`/`text`/`toolUseID`, `backup.realParentDir`, system `choice`/`persistedAsDefault`, user `classifierMetaLines`, and ScheduleWakeup sidecar keys). The regenerated baseline also dropped vocabulary whose transcripts have aged out of the local corpus (e.g. the `SendMessage`/`TaskOutput` tool names, system `error.*` retry keys, `document` content blocks); the adapter still parses those shapes, and their reappearance would scan as additive drift, flagging for re-validation. Re-validated on 2.1.236 by drift probe: one new record type in the probe transcripts, `atis-latch` (keys `atis`, `sessionId`; `atis` observed only as an empty string; no model-visible content, skip-listed), and the local-corpus reconciliation surfaced two more sidecar types, `bridge-session` (2.1.236: cloud-bridge session linkage — keys `sessionId`, `bridgeSessionId`, `lastSequenceNum`, `ownerAccountUuid`, `ownerOrganizationUuid`) and `frame-link` (observed from 2.1.231: links a local file to a published artifact frame — keys `sessionId`, `path`, `frameUrl`, `title`, `timestamp`), both skip-listed. Additive corpus keys absorbed in the same regeneration (`attachment` lost `imagePasteIds` to corpus aging while `user` gained `queuePriority` and SendUserFile sidecar keys `toolUseResult.attachments`/`caption`/`display`/`liveSubscription`); the regenerated baseline again dropped vocabulary whose transcripts aged out (system `choice`/`persistedAsDefault`/`session_id`, the ScheduleWakeup sidecar keys) — the adapter still parses those shapes. Re-validated on 2.1.267 by drift probe (all six probes exercised and parsed): additive probe keys `perTurnEffort` and `apiBlockIndex` (assistant envelope), `wireToolInputs` and `wireIngestContext` (assistant maps keyed by tool_use id; the baseline collapses the ids to `wireToolInputs.*`/`wireIngestContext.*` so fresh sessions do not mint vocabulary), `rendered`, and a batch of `attachment.*` subkeys (`addedBlocks`, `autoModeConsentFlow`, `bashFirst`, `bashFirstSteer`, `bypass`, `cliPrefix`, `commit`, `context`, `date`, `entries`, `failedMcpServers`, `identity`, `model`, `pr`, `sendUserFileHint`, `snapshot`, `steerOnly`, `surfacedNames`, `systemPrompt`, `tools`, `url`, `wireHiddenNames`). The local-corpus reconciliation surfaced one new record type, `cost-state` (interactive sessions only; see the session accounting section below: it carries usage telemetry, so it becomes a `system` event rather than joining the skip list; before this it failed the strict parse), plus corpus keys `user.turnCompanion`/`queueSkipAttachments`/`imagePasteIds`, sidecar hints `toolUseResult.{ghRateLimitHint, retrieval_status, staleReadFileStateHint, timedOutAfterMs}`, `queue-operation.reason`, `attachment.banner`/`source_uuid`/`imagePasteIds`/`nameOnlyAnnouncements`, `renderedInHumanTurn`, and the `TaskOutput` tool name back from corpus aging (now mapped to kind `read`). No vocabulary dropped in this regeneration.

This grounds the `session` schema in what Claude Code actually writes, and records the normalization rules the `harness/claudecode` adapter must implement. Numbers below are from this sample; they describe presence and shape, not guarantees.

## File layout

- One project directory per working directory (path-encoded name).
- Main sessions: `<session-uuid>.jsonl`.
- Subagent sessions: `agent-<agentId>.jsonl` under `<project>/<session-uuid>/subagents/`, i.e. nested beneath the parent session, not siblings of it. Every record in them has `isSidechain: true` and an `agentId`; their `sessionId` points at the parent session. In this sample, sidechain records appear *only* in agent files, never inline in the session file (older versions reportedly inlined them; unverified, and the loud-failure rule will surface it if we hit one).
- Records are strict JSONL, one complete JSON object per line. Genuine streaming parsing is possible.

## Top-level record types

Ten `type` values observed, in two clear families, plus (from 2.1.267) one session accounting record that fits neither.

### Conversation records (become events)

| type | n | notes |
| --- | --- | --- |
| `assistant` | 1,766 | One record per content block of an API message (see splitting, below) |
| `user` | 989 | Real user prompts *and* tool-result delivery records |
| `system` | 148 | Subtypes: `turn_duration` (125), `away_summary` (21), `api_error` (2). First-consumer smoke tests on 2.1.197 additionally observed `model_refusal_fallback` (see error shapes, below). From 2.1.205, `turn_duration` records can carry `pendingBackgroundAgentCount` |
| `attachment` | 177 | Harness-injected context: `task_reminder`, `skill_listing`, `deferred_tools_delta`, `queued_command`, `agent_listing_delta`, `date_change`, `diagnostics`, `command_permissions`, `edited_text_file`. A drift probe on 2.1.197 additionally observed the envelope key `attachment.needsAuthMcpServers` in headless runs; additive, absorbed into the vocabulary baseline. |

### Harness/UI state records (documented skip list, counted in the parse report)

| type | n | content |
| --- | --- | --- |
| `mode` / `permission-mode` | 229 each | UI mode toggles |
| `ai-title` | 227 | Generated session title |
| `last-prompt` | 219 | Pointer to latest prompt (leafUuid) |
| `file-history-snapshot` | 196 | Checkpointing metadata for file rollback |
| `file-history-delta` | — | Per-file backup record for the rollback feature (2.1.212): `messageId`, `snapshotMessageId`, `trackingPath`, `backup{backupFileName (nullable), version, backupTime}`, `timestamp`. No `sessionId`, like `file-history-snapshot` |
| `queue-operation` | 40 | Prompt queue enqueue/dequeue/remove |
| `pr-link` | — | Session-to-PR linkage (2.1.220): `sessionId`, `prNumber`, `prUrl`, `prRepository`, `timestamp`. Written when a session creates or attaches to a GitHub PR; backs `--from-pr` resume |
| `atis-latch` | — | Sidecar latch record (2.1.236): `sessionId`, `atis` (observed only as an empty string). Purpose unknown; no model-visible content |
| `bridge-session` | — | Cloud-bridge session linkage (2.1.236): `sessionId`, `bridgeSessionId` (`cse_` prefix), `lastSequenceNum`, `ownerAccountUuid`, `ownerOrganizationUuid`. Written when a session is bridged to claude.ai/code |
| `frame-link` | — | Local-file-to-artifact linkage (2.1.231): `sessionId`, `path`, `frameUrl` (claude.ai artifact URL), `title`, `timestamp`. Written when a session publishes a file as an artifact |

These carry no model-visible content. Proposal: exclude from the event stream as an *explicit, enumerated* skip list, with counts surfaced in a parse report so nothing is silently dropped. Any `type` outside the known list is an error (or an `unknown` event in permissive mode).

### Session accounting record (becomes a `system` event)

| type | n | content |
| --- | --- | --- |
| `cost-state` | 6 (across 5 interactive 2.1.267 sessions) | The session cost tracker (the data behind `/cost`), persisted at orderly shutdown, once per process: `sessionId`, `totalCostUSD`, `totalAPIDuration`, `totalAPIDurationWithoutRetries`, `totalToolDuration`, `totalDuration` (all durations in ms), `totalLinesAdded`, `totalLinesRemoved`, `startTime` (epoch ms), `modelUsage{<model>: {inputTokens, outputTokens, thinkingTokens, cacheReadInputTokens, cacheCreationInputTokens, webSearchRequests, costUSD}}`, `hasUnknownModelCost`. No `uuid`, `parentUuid`, or `timestamp` |

Findings from three organic sessions plus three scripted probes on 2026-09-20 (all `entrypoint: "cli"`; the probes were a resume, a subagent spawn, and a `kill -9`):

- Written at **orderly shutdown**, once per process; Ctrl-C counts (the three organic sessions all exited that way, and there it is the last record). It is usually the last record, but not reliably: the `/exit` command's own local-command echo records (three `user` records) can be flushed after it, and a resumed session appends everything that follows after it. A hard kill (`kill -9`) writes nothing, and the file simply ends at the last bookkeeping record. Headless runs do not write it: nine `sdk-cli` sessions on the same version (probe transcripts and a scripted `-p` check) end with `last-prompt`, `atis-latch`, or `mode` and never carry it. The two interactive 2.1.236 sessions in the corpus lack it, so it is new in the 2.1.236–2.1.267 window.
- **Resume is cumulative.** A session exited and resumed with `--continue` holds two records in one file (one per process exit), and the second carries the first process's numbers forward: `startTime` stays the original process start, the helper model's usage is carried over unchanged (the resumed process made no helper call of its own), and `totalCostUSD` grows. Consumers wanting the session total take the **last** record. `totalDuration` is consistent with each process's own wall time summed (the gap between exit and resume is not counted), so `startTime + totalDuration` is the exit time only for a never-resumed session (verified exactly on the single-process samples).
- **Subagent usage is included**, keyed by model like everything else. A subagent forced onto `claude-sonnet-5` produced a `modelUsage` entry (2/3/0/34442 input/output/cache-read/cache-write) that matches the final usage snapshot in its `subagents/` transcript exactly, while the main transcript records no sonnet call.
- `modelUsage` is otherwise a **superset of the transcript**, which is why it is preserved rather than reconstructed: a helper model (`claude-haiku-4-5-20251001`) appears in every sample and never as an assistant record (title generation and similar calls), `thinkingTokens` has no counterpart in `message.usage`, and even the main model's counts exceed the sum of the transcript's final per-message usage snapshots plus its subagents' (in the organic sessions output within 0.5%, cache reads 10–20% higher, uncached input roughly 2×; the probes show the same gap in miniature). The adapter therefore treats it as telemetry: `Totals` stay derived from message usage, and adding the two together double-counts.
- **The tracker is restored from the transcript itself**, verified against the 2.1.267 bundle: its transcript loader treats `cost-state` as a "last-wins" record type keyed by `sessionId`, validates each candidate against a schema (every numeric field non-negative and finite, `modelUsage` keys free of control characters; an invalid record is dropped rather than restored), and keeps the last valid one per session. Cost is a purely client-side computation (hence `hasUnknownModelCost`); nothing comes from the API. A second copy of the same numbers is written to `~/.claude.json` as per-project `lastCost`, `lastModelUsage`, `lastStartTime`, `lastDuration`, `lastTotal*Tokens`, and `lastSessionId`, overwritten at every exit: a "last session in this project" summary, not a per-session store. Consequence for the "last record is the session total" rule: a process that dies without writing one (hard kill) is not restored by the next resume, so every later cumulative record under-counts by that process's usage, and the transcript's assistant records are the only trace of it.

## Common envelope (conversation records)

Every conversation record carries: `uuid`, `parentUuid` (threading chain; null at file root), `timestamp` (ISO 8601 UTC with ms), `sessionId`, `version` (harness version, per record), `cwd`, `gitBranch`, `userType` (`external`), `entrypoint` (`cli`), `isSidechain`. Agent files add `agentId`. From 2.1.204, `assistant`/`user`/`attachment` records also carry `session_id`, a snake_case duplicate of `sessionId` (observed identical; the adapter keeps reading `sessionId`). From 2.1.212, `assistant` records carry `effort` (the reasoning-effort level; sole observed value `"high"`). Interactive 2.1.205-era `user` records can carry `imagePasteIds` (int array) and `interruptedMessageId`; all three are additive envelope keys the adapter leaves unread. This is where `session_meta` comes from: constant-per-file fields lift into meta; `cwd`/`gitBranch` can in principle change mid-session, so the adapter should verify or track them.

## Key structural findings

### 1. Assistant API messages are split across records

One API message (`message.id`) spans 1 to 7 consecutive `assistant` records in this sample, roughly one content block each. `stop_reason` is null on non-final records (317 nulls observed; final values: `tool_use`, `end_turn`, `stop_sequence`).

**Usage is a growing snapshot, not a per-record delta.** In 77 of 551 multi-record messages, usage differs across records of the same `message.id` (`output_tokens` grows; input/cache fields stay fixed). Token accounting rule: **take the last record's usage per `message.id`; never sum across records.**

Adapter rule: fold records sharing `message.id`, preserving block order. Emit as ordered events: `thinking` / `assistant_message` (text) / `tool_call` (one per `tool_use` block), all carrying the shared `message_id` for correlation.

### 2. Tool calls and results live in different record types

- `tool_use` blocks appear in `assistant` records: `id`, `name`, `input` (full input, complete on the record; no delta assembly needed).
- `tool_result` blocks appear in `user` records: `tool_use_id` (correlation key), `content` (string, or block list; `list[text]` and `list[tool_reference]` observed), optional `is_error` (17 true / 190 false-explicit / 615 absent).
- Correlation via `tool_use_id` resolved in-sample with 1 orphan out of 818, so near-perfect but not guaranteed; the accumulator's pairing helper must tolerate orphans.
- Tool names seen: Bash, WebSearch, Edit, Read, WebFetch, Write, ToolSearch, Agent, Skill, Monitor, AskUserQuestion. The first consumer's smoke tests on 2.1.197 additionally observed Glob and Grep (the original corpus happened not to exercise them; the drift probe recipes now include a search probe so they stay covered), and the 2.1.205 corpus reconciliation added SendMessage and TaskOutput (agent-orchestration family; sidecar shapes in the table below).

### 3. The `toolUseResult` sidecar is an enrichment goldmine

Tool-result `user` records often (557/818) carry a top-level `toolUseResult` with *structured* per-tool data, richer than the `tool_result` block the model saw:

| Tool | sidecar keys |
| --- | --- |
| Bash | `stdout`, `stderr`, `interrupted`, `isImage`, `noOutputExpected`, `backgroundTaskId`, `persistedOutputPath`, `dangerouslyDisableSandbox` (bool; 2.1.205), `gitOperation` (2.1.212; structured git metadata on commit/push commands: `commit{sha, kind}`, `push{branch}`), `backgroundCwdHint` (str; 2.1.212, background tasks) |
| Read | `type`, `file` (path, full content) |
| Edit | `filePath`, `oldString`, `newString`, `originalFile`, `structuredPatch`, `userModified`, `replaceAll`, `staleRecovered` (bool; 2.1.205) |
| Write | `filePath`, `content`, `structuredPatch`, `originalFile`, `userModified` |
| WebFetch | `bytes`, `code`, `codeText`, `url`, `durationMs`, `result` |
| WebSearch | `query`, `results`, `durationSeconds`, `searchCount` |
| Grep | `content`, `filenames`, `mode`, `numFiles`, `numLines` (observed 2.1.197, smoke tests), `totalFiles` (2.1.212; observed with `mode: "files_with_matches"`) |
| Glob | `countIsComplete`, `durationMs`, `filenames`, `numFiles`, `totalMatches`, `truncated` (observed 2.1.197, smoke tests) |
| Agent | Two disjoint shapes keyed by `status`. Synchronous completion (`status: "completed"`): `agentId`, `agentType`, `content`, `prompt`, `resolvedModel`, `status`, `toolStats`, `totalDurationMs`, `totalTokens`, `totalToolUseCount`, `usage` (n=9). Background spawn acknowledgement (`status: "async_launched"`): `agentId`, `canReadOutputFile`, `description`, `isAsync` (true), `outputFile`, `prompt`, `resolvedModel`, `status` (n=6). `outputFile` points outside `~/.claude/projects` (a per-session tasks dir under the OS temp root), so post-hoc availability is not guaranteed. (2.1.197) |
| SendMessage | `success` (bool), `message` (str), `resumedAgentId` (str; the continued agent, joinable against Agent results' `agentId`) (n=1, 2.1.197) |
| TaskOutput | `retrieval_status` (str, `"success"` observed), `task` (object) (n=1, 2.1.197; input `task_id`/`block`/`timeout` — reads a background task's output) |

Presence varies (version- and tool-dependent; sometimes a bare string). Schema treatment: optional, harness-specific enrichment attached to `tool_result` events as raw JSON plus a few promoted fields, never required.

Baseline reconciliation over the local corpus (alongside the smoke-test findings) also absorbed the agent-orchestration vocabulary now in the table: the `SendMessage` and `TaskOutput` tool names, the Agent tool's two result shapes, and their sidecar keys. All of it belongs to one family — spawning, messaging, and reading the output of subagents/background tasks — and the adapter classifies the whole family as `other` (consistent with Agent, Skill, Monitor, AskUserQuestion; ACP's kind vocabulary has no delegation kind). Sample sizes are thin where noted (the SendMessage and TaskOutput observations are single instances, produced by the drift probe's own search-probe retry and a background-task session respectively), so treat those key sets as observed-minimum, not complete.

**RQ3 payoff, worth calling out:** for WebFetch, `toolUseResult.bytes` is the raw fetch size, while the `tool_result` block content is what the model actually received, and it is small (median ~900 chars, max ~2.5KB in-sample): direct evidence of the summarization pipeline. The block content vs. `bytes` ratio quantifies compression-by-pipeline per fetch. `url` and `durationMs` come free.

### 4. Subagent linkage

Agent files are self-contained sessions (root record has `parentUuid: null`). Linkage back to the parent: the filename/`agentId`, `sessionId` (parent session), and `promptId` shared with the spawning turn. Tool-result records in the parent carry `sourceToolAssistantUUID` (818/989) pointing at the assistant record that issued the tool call. This supports the planned flatten-with-`subagent_id` model, or full nested-session reconstruction later; the adapter should surface `agentId` + parent `sessionId` in `session_meta` and leave joining to the consumer or a helper.

### 5. Content block vocabulary

- `assistant`: `tool_use` (818), `thinking` (506; keys `thinking`, `signature`), `text` (442), and `fallback` (observed 2.1.197, web-html smoke test): `{"type": "fallback", "from": {"model": ...}, "to": {"model": ...}}` records a mid-message model switch. It arrives as its own split record, the first of its message; `message.model` on that record and the rest of the message is already the to-model, and `message.usage.iterations[]` stamps per-attempt usage with `type` `message` vs `fallback_message` and the attempt's model. The adapter surfaces it as a `system` event (subtype `assistant/fallback`, block verbatim in details); the from-model never appears on an assistant anchor, so consumers detecting fallback should look for this event, not compare anchor models. Note: in every observed transcript the `thinking` text is **empty**; only the opaque `signature` is persisted. Verified exhaustively: every thinking block in every record carries exactly `{type, thinking: "", signature}` (no alternate key holds the text), no thinking/reasoning-named field anywhere in any record carries content, blocks are never repeated in fuller versions across a message's split records (block instances sum exactly to record count), and no sibling file under `~/.claude/` (sessions, debug, file-history) holds it. The text is never written to disk in these versions; not recoverable post-hoc (same practical outcome as Codex's encrypted reasoning).
- `user`: `tool_result` (818), bare-string content (157), `image` (26), `text` (8), `document` (3).
- Some `user` records are harness-injected rather than human: `isMeta: true` (11), plus all `attachment` records. `promptSource` (`typed`/`queued`) and `origin` (`{kind: human}`) distinguish real human input where present. The schema needs an origin marker on `user_message` (human vs. harness-injected) or RQ4's "what did the user ask" queries will over-count.

### 6. Usage vocabulary (assistant `message.usage`)

Always present: `input_tokens`, `output_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens`, `service_tier`, `cache_creation` (breakdown), `inference_geo`. Often: `server_tool_use`, `iterations`, `speed`. Maps cleanly onto OTel GenAI semconv (`gen_ai.usage.input_tokens`, `gen_ai.usage.output_tokens`); the rest ride along as harness-specific extension fields.

### 7. Error shapes

- API errors: `assistant` records with `error`, `isApiErrorMessage`, `apiErrorStatus` (3 observed); `system` records with `subtype: api_error`, `level: error`, `retryInMs`, `retryAttempt`, `maxRetries` (2 observed).
- Tool errors: `is_error: true` on `tool_result` blocks.
- Tool denials (observed from 2.1.204, absorbed in the 2.1.205 reconciliation): the `user` record delivering a permission-denied `tool_result` (which is also `is_error: true`) carries a top-level `toolDenialKind`; sole observed value `user-rejected`. Additive envelope key: the denial is already visible through `is_error`, and the adapter leaves the key unread. The fixture's orphan error result now carries it so the hermetic corpus exercises the shape.
- Both must normalize to visible event-level error fields; retries are RQ4 data.
- **Refusal/model-fallback/retraction flow** (observed 2.1.197, first-consumer smoke tests): a `system` record with `subtype: model_refusal_fallback`, `level: warning`, and the keys `apiRefusalCategory`, `apiRefusalExplanation`, `direction`, `fallbackModel`, `originalModel`, `refusedUserMessageUuid`, `requestId`, `retractedMessageUuids`, `trigger` records a safeguards refusal that switched models and retracted earlier records; the retrying `assistant` record carries `supersedesUuids` (the retracted records stay in the file). Parses cleanly as a `system` event; a consumer replaying the thread should honor `supersedesUuids`/`retractedMessageUuids` if it wants the model-visible view. The `fallback` content block (see content block vocabulary) is the sibling mechanism at the message level; the same session can carry both.

## Known but unobserved in this sample

To handle defensively (loud error or verified mapping once fixtures exist): compaction/summary records (`summary` type, compact boundaries), none present in these 33 files; inline sidechains from older versions; `progress` records from Task-tool streaming, if they still exist. The experiment's pinned harness version makes this tractable: we verify against that version's fixtures and error loudly elsewhere.

## Proposed event mapping (draft for the `session` package)

| Native | Event kind | ACP analog | Notes |
| --- | --- | --- | --- |
| `user` record, human content | `user_message` | `user_message_chunk` | Origin marker: human |
| `user` record, `isMeta`/injected; `attachment` record | `user_message` or `system` (open question below) | none (extension) | Origin marker: harness |
| `assistant` text blocks (folded per `message.id`) | `assistant_message` | `agent_message_chunk` | Aggregated, not chunked |
| `assistant` thinking blocks | `thinking` | `agent_thought_chunk` | `signature` kept as extension field |
| `assistant` `tool_use` block | `tool_call` | `tool_call` | Full input inline |
| `user` `tool_result` block | `tool_result` | `tool_call_update` | + optional `toolUseResult` enrichment |
| `system` record | `system` | none (extension) | Subtype preserved |
| `cost-state` record (2.1.267+) | `system` (subtype `cost-state`) | none (extension) | Record verbatim in `details`; telemetry, never folded into `totals` |
| UI state records (11 types) | skip list | none | Counted in parse report |
| Anything else | error / `unknown` | none | Loud by default |

Extension fields everywhere: timestamps, `uuid`/`parentUuid` threading, source line provenance, sidechain identity, usage beyond the OTel pair.

## Open questions carried forward

1. Do harness-injected context records (`attachment`, `isMeta` user records) become `system` events or origin-marked `user_message` events? Leaning `system`: they are not user intent, but they are model-visible context, which `system` already means here.
2. `toolUseResult` promoted fields: promote only the cross-tool useful ones (`is_error` context, bytes, duration, url) and keep the rest as raw JSON? Leaning yes, promote minimally.
3. Whether `file-history-snapshot` ever matters for analysis (it records file states at checkpoints). Skip-listed for now; revisit if rollback behavior becomes a research question.
