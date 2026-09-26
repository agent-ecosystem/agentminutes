# Changelog

Notable changes to agentminutes. Each version covers the Go module, the
CLI, and the npm/PyPI wrappers together (wrapper versions always match
the Go tag). Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- Copilot CLI: a repeat activation of a skill whose body is unchanged,
  which the harness logs by reference (`skill.invoked_ref`: content
  hash and length, no body) and delivers again, is now a `system` event
  whose `text` is the body resolved from the earlier `skill.invoked`
  with that hash, in either text form, so the second delivery counts
  and traces like the first. A ref no record in the transcript
  resolves keeps an empty `text` with the ref fields in `details`
  rather than being dropped. The record type joins the drift baseline
  (regenerated over six further 1.0.88 transcripts). Reported in
  issue #4.

### Changed

- Copilot CLI: a skill activation's bare `text` now carries the content
  lines of the `<skill-context>` wrapper ahead of the SKILL.md body: the
  skill's base directory and, when the skill directory holds other
  files, the list of every file under it, which the harness composes
  for the model and which was previously reachable only through
  `--text-form delivered`. Only the wrapper's tag lines are left to the
  delivered form, matching how Claude Code's `<system-reminder>` tags
  are stripped. A consumer comparing the bare text to the body alone
  sees the prefix now; the body is still the text's tail.
- Copilot CLI: the delivery-marker test holds each
  `skill.context_delivered_ref` to the activation event just before it,
  in the parsed text form, instead of to any earlier skill body.

## [0.7.0] - 2026-09-25

Schema `0.2.0`: the `details` of a Claude Code attachment system event
changes shape (below), and Codex sessions gain a system event.

### Added

- Codex: the system prompt (`session_meta.base_instructions.text`) is
  now a `system` event of subtype `session_meta/base_instructions`, on
  the meta record's line with the prompt as `text`, matching how the
  Copilot CLI and Antigravity adapters surface their system prompt
  records. Sessions gain one system event; `stats.system_by_subtype`
  shows it.
- `convert --text-form delivered` (library: `harness.Options.TextForm`)
  puts the delivered form of injected text in a system event's `text`,
  wrapper included, where the transcript records it: Claude Code's
  rendered `<system-reminder>` blocks and Copilot's `<skill-context>`
  wrapper (the body is paired with its delivery record by the SHA-256
  that record carries). The default stays bare.
- A text-surfacing invariant for adapter tests (`internal/textaudit`):
  a `system` event or tool result whose details (enrichment) carry a
  long string either surfaces it (its text contains or is contained by
  the string) or is on the adapter's enumerated non-text list with a
  reason, and where a harness records that it delivered text (Claude
  Code's rendered attachments, Copilot's skill delivery hash) a
  threshold-free check holds the adapter to that record. Runs over every
  fixture in both text forms and, env-gated, the local corpus, so a
  harness release that adds or moves injected text, or a result whose
  sidecar carries text its content does not, fails loudly instead of
  being absorbed.

### Fixed

- Copilot CLI: `skill.invoked` system events now carry the delivered
  SKILL.md body as `text` (it was only in `details`), so consumers that
  trace model-visible text see a skill load as harness-injected content
  rather than only at model output (#3). The body is byte-exact what the
  harness's `skill.context_delivered_ref` hashes as delivered.
- Claude Code: attachments that carry their injected text under `text`
  (`model`, `total_tokens_reminder`, `batching_reminder_sent`,
  `silent_turn_reminder`, 2.1.27x) now surface it as the system event's
  `text`; only `content` was read before. `prompt_snapshot` surfaces
  the system prompt (its sections joined by a blank line, as the
  harness sends them, without the dynamic boundary sentinel); `instructions` the CLAUDE.md bodies;
  `queued_command` the queued prompt or task notification;
  `edited_text_file` the snippet; `read_truncation_notice` the banner;
  and the agent, tool, and MCP-instruction deltas their added lines.
  When a record carries its rendered form, the `<system-reminder>`
  block as injected (2.1.267+), that is the text with the tags
  stripped: the fields do not always reconstruct the injected message
  (`deferred_tools_delta` renders two lists from two fields), and for
  the structured attachments (`environment`, `session_context`,
  `auto_mode`, `date`, `remote_session_change`,
  `bash_output_audience_note`) it is the only record of the text at
  all. The fields remain the fallback for older records. A task
  notification's two renderings (mid-turn and in the human's turn) are
  chosen by position with the harness's own predicate.

### Changed

- Claude Code: an attachment system event's `details` is now the whole
  record, as it already was for `system` records, rather than the
  attachment object alone. Consumers reading attachment fields from
  `details` find them under `attachment.<key>`; the envelope's
  `rendered[].content` (2.1.274+, the `<system-reminder>` wrapper as
  delivered) is now preserved.

## [0.6.0] - 2026-09-25

### Added

- Task summaries. `stats --include-subagents` (library:
  `agentminutes.Task`) treats a transcript as a task's parent, gathers
  the subagent transcripts the harness wrote for it through the new
  `harness.Locator.Gather` (Claude Code by layout, Codex by the
  in-band root session id, Antigravity by the conversation ids in
  `invoke_subagent` results, Copilot CLI inline), and reports each
  transcript's summary, a task aggregate in the `Stats` shape
  (`session.SumStats`, with `total_prompt_tokens` re-derived per
  harness), and a per-agent split. `Stats` gains `by_agent` (schema
  extension, additive): the split per agent id when a session's events
  span more than one agent, the parent under the empty key. The
  grouping is pinned three ways: locator fixtures per harness, an
  env-gated corpus invariant that every subagent transcript discovery
  finds is claimed by exactly one task, and the drift probe's subagent
  task now asserting the join and that task totals equal the sum of
  the transcripts' totals on fresh delegations. What a gather could not
  include (an unreadable candidate rollout, a child conversation no
  longer in the store) is listed in `skipped`, never dropped.
  **For implementers of `harness.Locator` outside this module:** the
  interface gained the `Gather` method, so external locators must add
  it (returning the parent alone, with `harness.JoinInline`, is a valid
  minimal implementation).

- GitHub Copilot CLI support (`copilot`), validated against 1.0.88
  transcripts (`~/.copilot/session-state/<session-id>/events.jsonl`).
  `session.start` becomes `session_meta` (session id, Copilot version,
  cwd, git branch); `user.message` and `assistant.message` become the
  conversation, with provider-native reasoning blocks as `thinking`
  events and `toolRequests` as `tool_call`s sharing the message id;
  `tool.execution_complete` becomes the `tool_result` (denied calls are
  errors carrying the denial message; `web_fetch` results promote the
  URL and HTTP status to `fetch`). Reasoning is mapped for every model
  family the CLI offers, validated per family (Anthropic thinking
  blocks with signatures; the OpenAI Responses encrypted reasoning
  blocks that GPT, Grok, and MAI models share; the flattened
  `reasoningText`/`reasoningOpaque` pair Gemini and Kimi record). Subagents (`task`,
  `search_code_subagent`) are recorded inside the parent transcript:
  their conversations parse as ordinary events attributed by the new
  `agent_id` event field, their prompts have origin `harness`, and the
  `subagent.*` lifecycle records are `system` events. Every built-in tool on 1.0.88 was
  exercised, and six fixtures pin the shapes: the base conversation,
  sync subagents, parallel/background/nested/custom subagents, the
  file/search/shell/fetch/MCP tools, every tool-failure shape (a tool
  that could not run has `error.code` `failure`; a shell command that
  exited nonzero is `success: true` with the exit code, and both parse
  as failed results) plus an image viewed with `view` (a
  `session.binary_asset` record, surfaced as an `image` content block
  on the result), and skills (`skill.invoked` and
  `skill.context_delivered_ref` records) with the sql, documentation,
  and user-configured MCP tools. Every other record (turn
  boundaries, permission prompts, `system.message`, usage checkpoints,
  shutdown, resume, abort) is a `system` event with the payload
  verbatim, and unknown `session.*`, `permission.*`, `skill.*`, and `subagent.*`
  subtypes map there too. The format records no per-message usage, so `totals` is
  omitted; the session-cumulative per-model numbers live in the
  `session.shutdown` event's details. The locator scans the
  per-session directories (honoring `COPILOT_HOME`), accounts for every
  sidecar, and resolves a session id directly to its directory. The
  drift probe gains a Copilot search probe over the `glob`/`grep`
  tools.

- `agent_id` on events (schema extension, additive): set when a harness
  interleaves a subagent's conversation into the parent transcript,
  empty for the main agent and for harnesses whose subagents have their
  own transcripts. `SchemaVersion` stays 0.1.0.

### Changed

- Bumped the agentsummons dependency to v0.4.0, which adds Copilot CLI
  headless invocation.
- The Copilot probe round was repeated for the other three harnesses
  (failures, images, MCP, skills, subagents, and on Antigravity the
  Claude and GPT-OSS model APIs), and the baselines were regenerated
  over the fixtures plus the local corpora. Two adapters changed:
  codex now classifies a unified-exec command that exits nonzero or an
  `apply_patch` that fails ("Script failed") as a failed result (before,
  no 0.15x failure was ever marked), and antigravity marks a
  `run_command` whose templated content reports a nonzero exit as
  failed and surfaces a `view_file` image (the new `media` key) as an
  `image` content block; `replace_file_content` maps to kind `edit`.
  Claude Code needed no change; its baseline gains the 2.1.274 peer
  messaging and hand-back vocabulary (`SendMessage`,
  `SubagentHandback`, `origin.handback`) from the interactive corpus,
  plus `NotebookEdit` and the first observed MCP tool call. New
  fixtures pin every shape (`claudecode/testdata/tools.jsonl`,
  `codex/testdata/probes.jsonl`, `antigravity/testdata/probes.jsonl`).
- `drift probe` gains two standard probes: "failure" (a nonzero exit
  and a read of an absent path, asserting a failed result) and a
  per-harness "subagent" probe asserting the harness's own delegation
  tool, so those vocabularies stay in the baselines.

## [0.5.2] - 2026-09-25

### Changed

- Revalidated against antigravity 1.2.11, claude-code 2.1.274, and codex
  0.157.0 (`LastValidated`); baselines regenerated over fixtures plus
  the local corpus. No adapter changes were needed. Antigravity was
  unchanged. Claude Code surfaced the Stop-hook records (`system`
  subtype `stop_hook_summary` and `attachment` type `hook_success`,
  both already parsed) and added `attachment.clearAt`,
  `managedCommit`/`managedPr` on `remote_session_change`, the
  `SendFeedback` tool (kind `other`), classifier-context keys, and the
  Bash sidecar `toolUseResult.bashEditDiff`, all additive. Codex added
  `event_msg.payload.root_turn_id` and
  `response_item.metadata.mcp_attribution`/`user_input_order`, all left
  unread; its file and fetch probes stay inconclusive by construction
  and both opt-in promotions still recover the edit and fetch.
- Bumped the agentsummons dependency to v0.3.5.

## [0.5.1] - 2026-09-20

### Added

- Claude Code `cost-state` records parse as `system` events (subtype
  `cost-state`, record verbatim in `details`). Interactive sessions on
  2.1.267 persist the session cost tracker at orderly exit, once per
  process: total cost, API/tool/wall durations, lines changed, and
  per-model token usage including thinking tokens, subagent calls, and
  helper-model calls that never appear as assistant records. A resumed
  session writes another, cumulative record at its own exit, so the
  session total is the last one. Because that usage is a superset of
  the transcript, it is preserved as telemetry and never folded into
  `totals`. Before this, such transcripts failed the strict parse with
  `unrecognized record type "cost-state"`.

### Changed

- Revalidated against antigravity 1.2.7, claude-code 2.1.267, and codex
  0.155.1 (`LastValidated`); baselines regenerated over fixtures plus
  the local corpus. Antigravity was unchanged. Claude Code added
  assistant keys (`perTurnEffort`, `apiBlockIndex`, `wireToolInputs`,
  `wireIngestContext`), a batch of `attachment` subkeys, and sidecar
  hints; the `TaskOutput` tool now maps to kind `read`. Codex added
  `response_item.metadata`, `session_meta.payload.runtime_workspace_roots`,
  and `turn_context.payload.disabled_plugin_ids`, all left unread; its
  file and fetch probes stay inconclusive by construction and both
  opt-in promotions still recover the edit and fetch.
- Baseline vocabulary collapses key paths that are per-record
  identifiers (`wireToolInputs.*`, `wireIngestContext.*`,
  `modelUsage.*`), so a fresh session or a new model name no longer
  reads as drift.
- agentsummons dependency bumped to v0.3.4.

## [0.5.0] - 2026-09-13

### Added

- Subagent sessions are now first-class for all three harnesses,
  validated by live spawn probes (codex 0.154.0, antigravity 1.2.2):
  - Codex multi-agent rollouts parse fully: the new
    `inter_agent_communication_metadata` record and `agent_message`
    response items (inter-agent messages with author/recipient agent
    paths) become `system` events, and the collaboration
    `function_call`s (`spawn_agent`, `wait_agent`) are the first
    observed `function_call` records. A subagent rollout's meta now
    carries `is_subagent` and `subagent_id` (from `thread_source` and
    the thread's own id); `session_id` records the root thread at every
    spawn depth, so grouping a task's files is a `session_id` match,
    and `Locate` accepts a subagent's own thread id. Fixtures
    `subagent_parent.jsonl`/`subagent_child.jsonl` pin the shapes.
  - Antigravity subagents (parent `invoke_subagent` tool call answered
    by a `GENERIC` step embedding the child conversation id; the child
    reports back via `send_message` with the parent id as recipient)
    already parsed; the baseline and fixture now pin the vocabulary.
    The transcript records no structural subagent marker, so
    `is_subagent` stays unset for antigravity; correlation is a
    content join, documented in the format inventory.
  - The `sessions` `--session-id` scan filter also matches
    `subagent_id`, so a codex subagent file is findable by its own
    thread id.

## [0.4.1] - 2026-09-13

### Changed

- Harness drift reconciled from a fresh probe run; `harness.LastValidated`
  moved to antigravity 1.2.2, claude-code 2.1.236, and codex 0.154.0, with
  baselines regenerated (antigravity's vocabulary was unchanged) and the
  format inventories updated:
  - Codex 0.154.0 adds a `token_usage_record` top-level record
    (per-response usage telemetry). It parses to a `system` event; its
    numbers duplicate the `token_count` stream, which remains the usage
    accounting source. `turn_context` gained an additive `root_turn_id`
    key. The fixture `harness/codex/testdata/usage_record.jsonl` pins the
    0.154.0 shape.
  - Claude Code adds three sidecar record types, all skip-listed:
    `atis-latch` (2.1.236), `bridge-session` (2.1.236: cloud-bridge
    session linkage), and `frame-link` (2.1.231: local-file-to-artifact
    linkage).
- Bumped the agentsummons dependency to v0.3.3 (validated harness-version
  refresh; no API changes).

## [0.4.0] - 2026-08-24

### Added

- Session totals carry a derived `total_prompt_tokens`: the
  convention-normalized total prompt size, comparable across harnesses
  by construction (Anthropic-style disjoint input fields sum;
  OpenAI-style `input_tokens` is already the total). The per-provider
  fields stay faithful to what the harness recorded; the new field is
  computed by the accumulator, tagged as a derived extension, and
  absent when a harness's convention is unknown. Additive extension
  field, so `SchemaVersion` stays 0.1.0. Resolves the cross-provider
  `input_tokens` comparability trap (#2).

### Fixed

- The codex adapter promotes `cache_write_input_tokens` into
  `cache_creation_input_tokens` instead of dropping it, so normalized
  codex sessions report cache writes without `--keep-raw` (#1). The
  field keeps codex's subset-of-`input_tokens` semantics, now
  documented on the schema's cache fields; `total_prompt_tokens` is the
  cross-harness comparable number.

- The npm wrapper ships the `agentminutes-win32-x64` platform package
  again (restored in the build matrix, `SUPPORTED`, and
  `optionalDependencies`): npm support resolved the registry naming
  block that forced its removal, so Windows x64 installs no longer need
  the PyPI package or a manually downloaded release binary.

### Changed

- Revalidated all three adapters by drift probe and local-corpus
  reconciliation; `harness.LastValidated` moved to antigravity 1.1.19,
  claude-code 2.1.231, and codex 0.149.1, with baselines re-stamped or
  regenerated to match.
- Codex 0.149.1 format drift reconciled: every rollout record gained an
  `ordinal`, and the `user_message`/`agent_message`/`patch_apply_end`/
  `web_search_end` event_msg subtypes were replaced by an
  `item_completed` stream carrying typed items (UserMessage,
  AgentMessage, CommandExecution, FileChange, Reasoning, Extension).
  `item_completed` records parse as system events (the existing
  unknown-subtype rule); the codex baseline now tracks
  `payload.item.type`/`payload.item.kind` as discriminators, and a new
  fixture pins the 0.149.1 shape.
- `codex.PromotePatchApply` and `codex.PromoteWebSearch` now also match
  the 0.149.1 `item_completed` shapes (a FileChange item promotes to the
  edit pair, an Extension item of kind web.search to the fetch pair),
  since the telemetry subtypes they matched are gone from 0.149.1
  transcripts; without this the transforms silently no-op there and
  edits/fetches are invisible to tool metrics. New markers:
  `event_msg/item_completed/FileChange` and
  `event_msg/item_completed/Extension`. Transform names and
  `--promote` values are unchanged.
- Claude Code sessions linked to a GitHub PR write a new `pr-link`
  record (observed from 2.1.220; backs `--from-pr` resume). It is
  harness bookkeeping with no model-visible content, so the adapter now
  skip-lists it (counted in the parse report) instead of failing the
  strict parse with "unrecognized record type".
- Bumped the agentsummons dependency to v0.3.2, whose flag surface was
  revalidated against the same harness releases.

- GitHub release notes now come from CHANGELOG.md: the release workflow
  extracts the tag's section and fails the release if it is missing
  (ported from agentsummons, including the trap where setting
  `changelog.disable` in goreleaser silently discards `--release-notes`).
- Bumped `goreleaser-action` to v7 in the release workflow to clear the
  Node 20 deprecation warning on GitHub Actions runners.

## [0.3.1] - 2026-08-03

### Added

- Docs site at agentminutes.dev; README trimmed to point at it.

### Changed

- Revalidated the antigravity adapter against agy 1.1.10 by clean drift
  probe: all five probes exercised and parsed, vocabulary unchanged, the
  baseline re-stamped, and `harness.LastValidated` bumped (claude-code
  2.1.212 and codex 0.146.0 unchanged).
- Bumped the agentsummons dependency to v0.3.1 (revalidated invocation
  flag surface for agy 1.1.10).

## [0.3.0] - 2026-07-31

### Added

- Claude Code: the new `file-history-delta` record type (rewind-feature
  bookkeeping, sibling of `file-history-snapshot`) joins the skip list
  and the Sniff special case; it previously failed strict parse loudly.

### Changed

- Revalidated all three adapters by drift probe against antigravity
  1.1.8, claude-code 2.1.212, and codex 0.146.0: additive keys absorbed
  across the board (`effort`, Bash/Grep sidecar fields, user-envelope
  fields, antigravity `exit_code`, codex audio telemetry arrays), all
  baselines regenerated, `harness.LastValidated` bumped.
- Bumped the agentsummons dependency to v0.3.0, which carries agy
  1.1.8's in-band `conversation_id` for antigravity resume; post-hoc
  time-window discovery is now only needed in text mode or on older agy
  releases.

## [0.2.0] - 2026-07-20

### Added

- npm and PyPI wrapper packages (`wrappers/`): they deliver the platform
  binary and a thin API over the CLI, published by the release workflow
  with versions matching the Go tag.

### Changed

- Revalidated adapters against antigravity 1.1.4 and codex 0.144.6.

## [0.1.0] - 2026-07-19

### Added

- Initial release: Go library and CLI (`convert`, `detect`, `drift`,
  `sessions`, `stats`) parsing Antigravity CLI, Claude Code, and Codex
  CLI session logs into a unified, versioned event schema with total
  line accounting, loud parse failures, and transcript discovery via
  per-harness locators.

[Unreleased]: https://github.com/agent-ecosystem/agentminutes/compare/v0.7.0...HEAD
[0.7.0]: https://github.com/agent-ecosystem/agentminutes/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/agent-ecosystem/agentminutes/compare/v0.5.2...v0.6.0
[0.5.2]: https://github.com/agent-ecosystem/agentminutes/compare/v0.5.1...v0.5.2
[0.5.1]: https://github.com/agent-ecosystem/agentminutes/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/agent-ecosystem/agentminutes/compare/v0.4.1...v0.5.0
[0.4.1]: https://github.com/agent-ecosystem/agentminutes/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/agent-ecosystem/agentminutes/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/agent-ecosystem/agentminutes/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/agent-ecosystem/agentminutes/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/agent-ecosystem/agentminutes/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/agent-ecosystem/agentminutes/releases/tag/v0.1.0
