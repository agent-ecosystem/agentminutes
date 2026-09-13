# Changelog

Notable changes to agentminutes. Each version covers the Go module, the
CLI, and the npm/PyPI wrappers together (wrapper versions always match
the Go tag). Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

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

[Unreleased]: https://github.com/agent-ecosystem/agentminutes/compare/v0.4.1...HEAD
[0.4.1]: https://github.com/agent-ecosystem/agentminutes/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/agent-ecosystem/agentminutes/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/agent-ecosystem/agentminutes/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/agent-ecosystem/agentminutes/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/agent-ecosystem/agentminutes/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/agent-ecosystem/agentminutes/releases/tag/v0.1.0
