# Codex CLI transcript format: empirical inventory

Status: findings
Source: four local rollout transcripts under `~/.codex/sessions/`: one older desktop-originated transcript from Codex 0.118.0-alpha.2 and three generated fresh for this inventory with `codex exec` on 0.144.1 (Q&A, shell execution, file write). Thinner evidence than the Claude Code inventory; unverified parts flagged below, loud failure covers the gaps. Re-validated on 0.146.0 by drift probe: two additive keys, `payload.audio` and `payload.local_audio` (empty arrays on `event_msg` `user_message` payloads, alongside the existing `images`/`local_images`); the file and fetch probes remain inconclusive by construction, unchanged from 0.144.6/0.144.1 (both actions still leave only telemetry, promotable via the opt-in transforms).

**Format drift is real and fast.** Between 0.118 and 0.144: shell execution moved from `function_call` to `custom_tool_call` (with a script-string input), tool outputs changed from strings to content-block arrays, a new top-level `world_state` record type appeared, `token_count` moved from per-turn to per-request, and `session_meta` gained fields (`session_id`, `history_mode`, `context_window`). Any adapter work on Codex should regenerate ground-truth transcripts first.

## File layout

- One file per session: `~/.codex/sessions/YYYY/MM/DD/rollout-<timestamp>-<uuid>.jsonl`.
- Strict JSONL. Every line is an envelope: `{"timestamp": "<RFC3339 ms Z>", "type": "<record type>", "payload": {...}}`.
- `~/.codex/session_index.jsonl` is a separate index, not a transcript.

## Record types

| type | Role | Disposition |
| --- | --- | --- |
| `session_meta` | Session identity: `session_id`/`id`, `cwd`, `cli_version`, `originator`, `source`, `git{}`, base instructions | First one feeds `session_meta`; later ones (resume?) become `system` events |
| `response_item` | The conversation: Responses API items exactly as the model saw/produced them | Canonical source of message/thinking/tool events |
| `event_msg` | Harness telemetry: turn boundaries, token counts, patch results, echoes of messages | `system` events, except duplicates (skip list) and `token_count` (also feeds usage) |
| `turn_context` | Per-turn config: `model`, sandbox/approval policy, cwd | `system` event; also the only source of the model name |
| `world_state` | Harness context snapshot (environments, filesystem, skills); new in ~0.144 | `system` event |
| `compacted` | Compaction marker (from Codex source; not observed locally) | `system` event, defensively |

Anything else: loud error (permissive: `unknown` event).

## response_item payload types

Observed: `message` (roles `developer`, `user`, `assistant`), `reasoning`, `custom_tool_call`, `custom_tool_call_output`, `web_search_call` (0.118 only).

From the Responses API vocabulary, expected but **not observed locally**: `function_call`, `function_call_output`, `local_shell_call`. Implemented from documented shapes. Unknown `response_item` types stay loud (conversation-affecting, unlike `event_msg`).

Mappings:

- **`message`**: content blocks `input_text`/`output_text` (text), `input_image`, others. Role determines the event:
  - `assistant` → `assistant_message` (see usage pooling below). `phase` (`final_answer`, `commentary`) not promoted; KeepRaw preserves it.
  - `user` → `user_message`. Origin heuristic: Codex injects harness content in the user role wrapped in known tags; a first block starting with `<environment_context>` or `<user_instructions>` marks origin `harness`, otherwise `human`. Documented limitation: a human pasting text starting with exactly those tags would be misclassified.
  - `developer` / `system` → `system` event, subtype `message/<role>` (permissions, app context, collaboration mode, skills; never user intent).
- **`reasoning`**: `thinking` event. Text = joined `content[]` texts, else joined `summary[]` texts; `encrypted_content` (the default; plaintext is usually absent) goes in `signature` as the provider's opaque form of the thought.
- **`custom_tool_call`**: `tool_call`. `call_id` correlates; observed `name` is `exec` with `input` a JavaScript-ish script string (`const r = await tools.exec_command({...})`), not JSON. Input decoding rule for all call types: if the input/arguments string parses as JSON, use the parsed value; otherwise encode it as a JSON string.
  - **Drift-probe finding (0.144.6)**: the unified `exec` tool now consistently wraps file edits too — the script calls `tools.apply_patch("*** Begin Patch ...")` and no `apply_patch`-named tool call appears (both probe attempts, so not model whim). The only structured record of the edit is the `event_msg` `patch_apply_end` telemetry (keys `call_id` of the form `exec-<uuid>`, `turn_id`, `stdout`, `stderr`, `success`, `changes{path: {type, content}}`, `status`), which was already known vocabulary at 0.144.1. Consequence: on 0.144.6 transcripts file edits are invisible as tool interactions (the exec wrapper maps to kind `execute`), and the drift probe's file expectation is inconclusive by construction on this version — the same shape the fetch expectation reached at 0.144.1. Resolved the same way: the opt-in transform `codex.PromotePatchApply` synthesizes the edit pair from `patch_apply_end`, `promoted_from`-marked. Payloads also carry a new `internal_chat_message_metadata_passthrough` key (`turn_id` inside).
- **`custom_tool_call_output`**: `tool_result`. `output` observed as an array of `input_text` blocks; older/other forms (plain string, object with `content`/`success`) handled too. Full payload in `enrichment`.
- **`function_call`/`function_call_output`** (unverified locally): same rules; `arguments` is a JSON-encoded string; output error detection from `success == false` or inner `metadata.exit_code != 0`.
- **`web_search_call`**: server-side tool: status and action in one record, no correlation id, and **the retrieved content never appears in the transcript** (relevant to RQ3: Codex web retrieval is invisible to post-hoc analysis, unlike Claude Code's WebFetch). Mapped as a synthesized `tool_call` + `tool_result` pair sharing a line-derived ID (`web_search_call:L<line>`), result content empty, payload in `enrichment`, `is_error` when status is not `completed`.
  - **Drift-probe finding (0.144.1)**: a URL fetch (`open_page`) on 0.144.1 produces **no `response_item` at all**; the only trace is an `event_msg` with `payload.type: "web_search_end"` (keys `call_id`, `query`, `action{type,url}`). The `response_item` `web_search_call` shape appears to be 0.118-only. Consequence: on 0.144 transcripts web retrieval is invisible even as a tool interaction (the telemetry becomes a `system` event via the unknown-subtype rule), and the drift probe's fetch expectation is inconclusive by construction on this version. Resolved: the opt-in transform `codex.PromoteWebSearch` synthesizes the fetch pair from `web_search_end` at the caller's request (never by default), marking the events `promoted_from` so double-counting on versions that emit both shapes stays auditable. Design record: `plans/telemetry-promotion.md`.

## event_msg payload types

Observed: `task_started`, `task_complete`, `user_message`, `agent_message`, `token_count`, `patch_apply_end`, and (via drift probe on 0.144.1) `web_search_end`, the only trace of a URL fetch on that version (see the `web_search_call` note above).

- **Skip list (duplicates/deltas, counted, never events):** `user_message`, `agent_message`, `agent_reasoning`, and `*_delta` types. `user_message`/`agent_message` duplicate the adjacent `response_item` byte-for-byte (confirmed 1:1 in fresh transcripts). Skip keys are namespaced: `event_msg/user_message`.
- **`token_count`**: usage (see pooling below); the full payload also becomes a `system` event so cumulative totals (`info.total_token_usage`) and rate limits stay available. `info` can be null.
- **Everything else, known and unknown subtypes** (`patch_apply_end`, future additions): `system` event, subtype = payload type, payload in Details. Unknown `event_msg` subtypes are deliberately *not* loud: they are telemetry, preserved in full, and the vocabulary churns per release. On 0.144.6 `patch_apply_end` is the only structured record of a file edit (see the `custom_tool_call` drift-probe finding above); `codex.PromotePatchApply` promotes it on request.
- `error`/`stream_error`: `system` event with level `error`.

## Structural findings

1. **Usage is per-request and arrives after content.** `token_count` fires after every API request (verified: one per request, and the sum of `last_token_usage` across a session equals the final `total_token_usage` exactly, in all three fresh transcripts). The adapter pools each `last_token_usage`; the next assistant anchor to close takes the pool as its usage. Consequences: a turn's tool-loop requests are attributed to the message they produced; session totals sum correctly; mid-turn commentary messages may carry nil usage; Extra fields (`reasoning_output_tokens`, `total_tokens`) are not pooled (available in the `token_count` system events).
2. **The anchor pattern applies, with different triggers.** An assistant `response_item` opens a fold; it closes on the next `response_item`, `task_started`, `task_complete` (closing before the boundary event is emitted), or EOF.
3. **The model name lives in `turn_context`, not on messages** (`gpt-5.3-codex` in April, `gpt-5.6-sol` in fresh transcripts). The parser tracks it and stamps assistant anchors at fold open.
4. **No per-record IDs.** Rollout lines carry no UUIDs; events have empty `id`/`message_id`, provenance lines are the stable reference. `turn_id` groups turns but is not a message identity, so it is not promoted to `message_id`.
5. **Reasoning is encrypted by default** (`encrypted_content` with empty `summary`). Plaintext thinking is mostly unavailable, limiting cross-harness thinking-content comparison.

## Known unknowns (revisit with more transcripts)

- `function_call`/`local_shell_call` flows: implemented from documented shapes, no local fixture.
- Compaction records and resumed/forked sessions (does a resumed rollout start with `session_meta`? Meta falls back to sparse if not).
- Whether `*_delta` event_msg types ever appear in persisted rollouts.
- MCP tool call shapes.
