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
| GitHub Copilot CLI | Supported | `~/.copilot/session-state/<session-id>/events.jsonl` |

## Validation coverage

Adapters are validated against real transcripts with a mechanical
line-accounting check. Every source line becomes an event, a counted skip,
or an error.

Two Antigravity caveats worth knowing: its transcripts carry no token
usage, and its tool calls have no correlation IDs (the adapter
synthesizes step-derived IDs and pairs positionally).

One Copilot CLI caveat: its transcripts record no per-message token
usage. The only usage numbers are session-cumulative, per-model totals
in the `session.shutdown` record (and a prompt-side snapshot of the
last API call in `session.usage_checkpoint`), so `totals` is omitted
and those records are preserved as `system` events with the numbers in
`details`. A resumed session writes another `session.shutdown` with
cumulative counts, so the session total is the last one. The event
schema itself is observed, not documented by GitHub; reasoning was
validated for every model family the CLI offers (Anthropic, OpenAI,
Google, xAI, Moonshot, Microsoft), which record it in three shapes;
a block shape outside those fails loudly until it is inventoried. Subagents are recorded inside the parent transcript and
attributed by `agent_id`, at any depth and including background and
parallel agents; see [Subagents](/docs/subagents/#github-copilot-cli).
Every built-in tool registered on 1.0.88 was exercised, including
failures: a tool that could not run, and a shell command that exited
nonzero, both parse as failed results.

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
