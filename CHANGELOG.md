# Changelog

Notable changes to agentminutes. Each version covers the Go module, the
CLI, and the npm/PyPI wrappers together (wrapper versions always match
the Go tag). Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Changed

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

[Unreleased]: https://github.com/agent-ecosystem/agentminutes/compare/v0.3.1...HEAD
[0.3.1]: https://github.com/agent-ecosystem/agentminutes/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/agent-ecosystem/agentminutes/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/agent-ecosystem/agentminutes/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/agent-ecosystem/agentminutes/releases/tag/v0.1.0
