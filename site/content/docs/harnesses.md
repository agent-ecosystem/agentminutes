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

Adapters are validated against real transcripts with two mechanical
checks. Line accounting: every source line becomes an event, a counted
skip, or an error. Text surfacing: every long string an event keeps in
its `details` or `enrichment` is either accounted for by the event's
`text` or content, or listed by the adapter with the reason it is not
model-visible text, and where a harness records that it delivered text
(Claude Code's rendered reminders, Copilot CLI's skill delivery hash)
the adapter is held to that record. See the
[schema page](/docs/schema/) for what `text` promises.

Every adapter was exercised against the same set of failing calls (a
command that exits nonzero, a read of an absent path, an edit whose
target text is missing, a fetch that 404s), and the first three parse
as failed results everywhere. The recording differs underneath: Claude
Code and Copilot CLI flag them natively; Codex 0.15x reports a nonzero
exit only inside the unified exec output and a failed patch only as
"Script failed" text, both of which the adapter reads; Antigravity
reports a failed command only in the templated "exited with code"
line, which the adapter reads, and other failures with an `error` key.
The 404 is where they part: flagged on Copilot CLI and Antigravity,
unflagged on Claude Code (the status lands in `fetch.status_code`), and
indistinguishable from success on Codex.

Two Claude Code details worth knowing. An attachment record (the
harness's injected context: instructions, reminders, listings) becomes
a `system` event whose `details` is the whole record and whose `text`
is the injected message, taken from the record's rendered
`<system-reminder>` block on 2.1.267 and later and from the
attachment's own field before that. A prompt the user typed while the
model was working is delivered the same way, so it is a `system` event
too, with its authorship in `details`; user-message counts follow the
harness's turn structure. A background task's notification carries
two renderings, and the adapter picks the one the harness sent by the
same position rule the harness uses.

One Codex detail: the system prompt travels in the `session_meta`
record, which becomes the `session_meta` event, so the adapter also
emits it as a `system` event of subtype `session_meta/base_instructions`
on the same line, with the prompt as `text`. Every Codex session has
one more `system` event than its record count suggests.

Two Antigravity caveats worth knowing: its transcripts carry no token
usage, and its tool calls have no correlation IDs (the adapter
synthesizes step-derived IDs and pairs positionally). Its `thinking`
key carries a summary on Gemini models and the full plaintext reasoning
on the Claude and GPT-OSS models it also serves.

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

A Copilot CLI skill activation becomes a `system` event of subtype
`skill.invoked` whose `text` is what the harness delivered: the
skill's base directory, a list of every file under the skill
directory when it holds any (the model learns of bundled files the
SKILL.md never mentions), and the SKILL.md body, with the
`<skill-context>` tag lines left to `--text-form delivered`. Activating
the same skill again with its body unchanged is logged by reference
(`skill.invoked_ref`, a content hash in place of the body) and
delivered again; the adapter resolves the hash to the earlier body, so
the second activation carries the same `text` as the first. The
delivery record that follows each activation (`skill.context_delivered_ref`)
stays textless; a test holds each one to the activation before it by
that hash.

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
