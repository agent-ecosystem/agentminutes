# Claude Code transcript format: empirical inventory

Status: findings
Source: exhaustive scan of 33 local JSONL transcripts (4,220 records, 0 parse errors) under `~/.claude/projects/`, harness versions 2.1.153, 2.1.177, 2.1.187, 2.1.197. Re-validated on 2.1.204 by a clean drift probe (all six probes exercised and parsed; vocabulary unchanged against the baseline). Re-validated on 2.1.205 by baseline reconciliation over the local corpus, triggered by `drift scan` flagging tool-denial transcripts: five additive keys absorbed (`toolDenialKind`, `session_id`, `pendingBackgroundAgentCount`, and the `dangerouslyDisableSandbox`/`staleRecovered` sidecar keys; each documented in its section below), no structural changes.

This grounds the `session` schema in what Claude Code actually writes, and records the normalization rules the `harness/claudecode` adapter must implement. Numbers below are from this sample; they describe presence and shape, not guarantees.

## File layout

- One project directory per working directory (path-encoded name).
- Main sessions: `<session-uuid>.jsonl`.
- Subagent sessions: `agent-<agentId>.jsonl` under `<project>/<session-uuid>/subagents/`, i.e. nested beneath the parent session, not siblings of it. Every record in them has `isSidechain: true` and an `agentId`; their `sessionId` points at the parent session. In this sample, sidechain records appear *only* in agent files, never inline in the session file (older versions reportedly inlined them; unverified, and the loud-failure rule will surface it if we hit one).
- Records are strict JSONL, one complete JSON object per line. Genuine streaming parsing is possible.

## Top-level record types

Ten `type` values observed, in two clear families.

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
| `queue-operation` | 40 | Prompt queue enqueue/dequeue/remove |

These carry no model-visible content. Proposal: exclude from the event stream as an *explicit, enumerated* skip list, with counts surfaced in a parse report so nothing is silently dropped. Any `type` outside the known ten is an error (or an `unknown` event in permissive mode).

## Common envelope (conversation records)

Every conversation record carries: `uuid`, `parentUuid` (threading chain; null at file root), `timestamp` (ISO 8601 UTC with ms), `sessionId`, `version` (harness version, per record), `cwd`, `gitBranch`, `userType` (`external`), `entrypoint` (`cli`), `isSidechain`. Agent files add `agentId`. From 2.1.204, `assistant`/`user`/`attachment` records also carry `session_id`, a snake_case duplicate of `sessionId` (observed identical; the adapter keeps reading `sessionId`). This is where `session_meta` comes from: constant-per-file fields lift into meta; `cwd`/`gitBranch` can in principle change mid-session, so the adapter should verify or track them.

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
| Bash | `stdout`, `stderr`, `interrupted`, `isImage`, `noOutputExpected`, `backgroundTaskId`, `persistedOutputPath`, `dangerouslyDisableSandbox` (bool; 2.1.205) |
| Read | `type`, `file` (path, full content) |
| Edit | `filePath`, `oldString`, `newString`, `originalFile`, `structuredPatch`, `userModified`, `replaceAll`, `staleRecovered` (bool; 2.1.205) |
| Write | `filePath`, `content`, `structuredPatch`, `originalFile`, `userModified` |
| WebFetch | `bytes`, `code`, `codeText`, `url`, `durationMs`, `result` |
| WebSearch | `query`, `results`, `durationSeconds`, `searchCount` |
| Grep | `content`, `filenames`, `mode`, `numFiles`, `numLines` (observed 2.1.197, smoke tests) |
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
| UI state records (6 types) | skip list | none | Counted in parse report |
| Anything else | error / `unknown` | none | Loud by default |

Extension fields everywhere: timestamps, `uuid`/`parentUuid` threading, source line provenance, sidechain identity, usage beyond the OTel pair.

## Open questions carried forward

1. Do harness-injected context records (`attachment`, `isMeta` user records) become `system` events or origin-marked `user_message` events? Leaning `system`: they are not user intent, but they are model-visible context, which `system` already means here.
2. `toolUseResult` promoted fields: promote only the cross-tool useful ones (`is_error` context, bytes, duration, url) and keep the rest as raw JSON? Leaning yes, promote minimally.
3. Whether `file-history-snapshot` ever matters for analysis (it records file states at checkpoints). Skip-listed for now; revisit if rollback behavior becomes a research question.
