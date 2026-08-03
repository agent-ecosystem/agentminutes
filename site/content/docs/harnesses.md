---
title: Harness Support
description: Supported harnesses, native formats, and validation coverage.
icon: hub
weight: 700
---

## Supported harnesses

| Harness | Status | Native format |
| --- | --- | --- |
| Antigravity CLI | Supported | `~/.gemini/antigravity-cli/brain/<id>/.system_generated/logs/transcript_full.jsonl` |
| Claude Code | Supported | `~/.claude/projects/<project>/*.jsonl` |
| Codex CLI | Supported | `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` |
| Gemini CLI (classic) | Not planned | Retired for individual users June 2026; Antigravity is its successor |

## Validation coverage

Adapters are validated against real transcripts with a mechanical
line-accounting check. Every source line becomes an event, a counted skip,
or an error.

Two Antigravity caveats worth knowing: its transcripts carry no token
usage, and its tool calls have no correlation IDs (the adapter
synthesizes step-derived IDs and pairs positionally).

## Format drift

When a transcript was written by a harness release newer than the last
one validated (`harness.LastValidated`), parse errors say so: the likely
cause is format drift, and the fix is usually an agentminutes update. To
check a transcript without parsing it, use `agentminutes drift scan`;
see [CLI](/docs/cli/#agentminutes-drift).

## Companion tooling

agentminutes reads transcripts after the fact; it never invokes a
harness. Its companion [agentsummons](https://agentsummons.dev) owns the
invocation side: agentsummons convenes the meeting, agentminutes takes
the minutes. An agentsummons `Result`'s session ID, timestamps, and
workdir are the inputs `Locate` and `Scan` need to find what the harness
wrote.
