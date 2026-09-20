---
title: Comparing Token Counts
description: What token numbers mean per harness, which ones are comparable, and which comparisons are traps.
icon: calculate
weight: 650
---

Token counts are the first thing stakeholders ask for and the easiest
thing to get wrong. Two transcripts from different harnesses both report
an `input_tokens` number, the fields have identical names in the
agentminutes schema, and a naive comparison of them can be off by four
orders of magnitude. This page explains where each number comes from,
which numbers are comparable across harnesses, and which comparisons
need more care than a single subtraction.

The short version:

- **`totals.total_prompt_tokens` is the cross-harness prompt-size
  number.** It is derived from each provider's documented convention so
  that it means the same thing everywhere.
- **Every other token field keeps its provider's native semantics.**
  That preserves fidelity to what the harness recorded, and it means
  `input_tokens` is never directly comparable across harnesses.
- **Token counts measure the whole stack, never the model alone.** The
  harness's injected context, tool verbosity, and caching strategy are
  all in the numbers.
- **Missing usage is recorded as missing.** `totals` is omitted
  entirely when a transcript records no usage (Antigravity), so a
  measured zero is never conflated with "no data".

## Where the numbers come from

Each harness records usage differently, and the adapters normalize the
mechanics without touching the semantics:

| Harness | Native usage record | How totals are built |
| --- | --- | --- |
| claude-code | A growing usage snapshot on each record of a split assistant message | The adapter takes the final snapshot per API message; message values sum to the session total |
| codex | An `event_msg` `token_count` per API request (0.154.0 adds a duplicative `token_usage_record`, preserved as telemetry and excluded from accounting) | Per-request usage pools onto the assistant message that closes the request loop; the per-request values sum to the cumulative total (verified empirically) |
| antigravity | None | `totals` is omitted; absence of data stays distinguishable from zero |

Exactly one `assistant_message` event exists per API message, so
per-message usage is always recoverable from the event stream when you
need finer grain than the session totals.

Interactive Claude Code sessions on 2.1.267 and later also end with a
`cost-state` record, which agentminutes preserves as a `system` event
with subtype `cost-state`. It carries the harness's own per-model usage
and cost (including thinking tokens and helper-model calls that never
appear as assistant records), so it is a superset of the transcript.
The adapter leaves it out of `totals` for that reason: compare the two
to size the hidden overhead, never add them together.

## The input-token trap

The providers define `input_tokens` differently, and the difference is
enormous whenever prompt caching is active (it almost always is):

- **Anthropic-style accounting (claude-code)** reports `input_tokens`
  as only the uncached remainder of the prompt. Cache reads and cache
  writes live in two additional fields, `cache_read_input_tokens` and
  `cache_creation_input_tokens`, and the three fields are disjoint. The
  full prompt is their sum.
- **OpenAI-style accounting (codex)** reports `input_tokens` as the
  total prompt. The cache fields are subsets of it: the native
  `cached_input_tokens` maps to `cache_read_input_tokens`, and the
  native `cache_write_input_tokens` maps to
  `cache_creation_input_tokens`.

The same warm-cache request therefore produces a tiny `input_tokens`
under Anthropic semantics and a large one under OpenAI semantics, with
neither number wrong. The
[worked comparison](/docs/example-comparison/#what-the-comparison-shows)
shows the trap on a real task: 4 versus 46,480 as recorded, and 43,873
versus 46,480 once both are read by their own conventions.

`totals.total_prompt_tokens` exists because of this trap. It is
computed by the accumulator from each harness's documented convention
(the disjoint fields sum under Anthropic semantics; `input_tokens` is
already the total under OpenAI semantics) and is absent when a
harness's convention is unknown, rather than guessed.

## What compares cleanly

- **Prompt volume:** `totals.total_prompt_tokens`. This is the number
  to put in a cross-harness table.
- **Output volume:** `totals.output_tokens` is the model's generated
  tokens under both conventions, with the reasoning caveat below.
- **Cache behavior, per harness:** `cache_read_input_tokens` and
  `cache_creation_input_tokens` carry the same meaning everywhere
  (tokens read from cache, tokens written to it), so cache hit
  profiles are comparable even though their relationship to
  `input_tokens` differs per provider.

## What needs care

- **Reasoning and thinking tokens are inside `output_tokens`.** Claude
  bills extended thinking as output tokens, and OpenAI includes
  reasoning tokens in `output_tokens` (Codex breaks the number out as
  `reasoning_output_tokens`, which the adapter preserves in the
  `token_count` system events' details rather than promoting it to the
  totals). Two harnesses with different reasoning-effort settings will
  show output-token differences that have nothing to do with answer
  length.
- **Tokens are volume, never cost.** Cache reads, cache writes, fresh
  input, and output are each priced differently, and the prices differ
  per provider and per model. agentminutes deliberately computes no
  cost figures; multiply the per-class token fields by your own price
  sheet, and keep the classes separate when you do.
- **The prompt includes the harness, so the comparison includes the
  harness.** Injected skill listings, tool schemas, world-state
  snapshots, and developer messages all count toward
  `total_prompt_tokens` before the human prompt does anything. A token
  difference between two harnesses on the same task measures their
  scaffolding as much as the model. The
  [worked example](/docs/example-comparison/) quantifies this context
  injection directly, and `stats`'s `system_by_subtype` shows what was
  injected.
- **Caching strategy is part of the number.** A harness that caches
  aggressively converts fresh input into cache reads across a session.
  Comparing single-turn sessions to long multi-turn sessions, or cold
  runs to warm ones, changes the cache mix and therefore any
  cost-weighted reading.
- **Absent usage means absent, never zero.** Antigravity transcripts
  record no usage anywhere, so their sessions have no `totals` at all.
  Averages over a mixed fleet must treat those sessions as unmeasured
  rather than free.
- **API retries and aborted turns still consume tokens.** Usage that
  never attached to an assistant message (an aborted turn, for
  example) stays out of `totals` for codex, and remains recoverable
  from the `token_count` system events. If a session's arithmetic looks
  short, check the report and the system events before assuming loss.

## Subagent usage lives in separate transcripts

When a harness spawns subagents, each subagent conversation is its own
API context with its own usage, recorded in its own transcript: Claude
Code writes them under `<project>/<session-id>/subagents/`, and Codex
writes each subagent thread as a sibling rollout file. The parent
transcript records the delegation calls and the text the subagents
returned; the tokens the subagents consumed appear only in the
subagent files. A session's `totals` therefore cover that one
transcript's API context alone.

The scale of the undercount is easy to underestimate. In a real
four-subagent session:

| Scope | Prompt tokens | Output tokens |
| --- | --- | --- |
| Parent transcript only | 2,622,260 | 49,993 |
| The four subagent transcripts | 1,519,301 | 48,616 |
| The whole task | 4,141,561 | 98,609 |

Reading only the parent file undercounts the task's prompt volume by 37
percent and misses half of its output tokens.

The schema makes the aggregation mechanical rather than forensic: a
subagent transcript's meta shares the parent's `session_id` and adds
`subagent_id` and `is_subagent: true`, and
[discovery](/docs/discovery/) treats the parent and its subagent files
as one session, so a single `sessions --session-id` call lists every
transcript the task touched:

```sh
agentminutes sessions --harness claude-code --session-id "$SESSION" |
  while read -r t; do
    agentminutes stats "$t" | jq '.totals.total_prompt_tokens // 0'
  done | jq -s add
```

Two caveats. First, decide which scope your metric wants before
comparing: "what did this conversation cost" (parent only) and "what
did this task cost" (parent plus subagents) are both legitimate
questions with answers that differ by large factors. Second, the
harnesses group differently: Claude Code and Codex both record the
parent's session id in every subagent transcript, so the recipe above
works for both (for Codex, gather with a scan rather than
`--harness codex --session-id`; see [Subagents](/docs/subagents/)),
while Antigravity records no usage at all, subagent or not.

## Recipes

Pull the comparable numbers for a set of sessions:

```sh
agentminutes sessions --since 2026-09-01 | while read -r t; do
  agentminutes stats "$t" | jq -r '[.totals.total_prompt_tokens // "n/a",
    .totals.output_tokens // "n/a", .models[0]] | @tsv'
done
```

Per-message usage, when session totals are too coarse:

```sh
agentminutes convert --format jsonl session.jsonl |
  jq -r 'select(.kind == "assistant_message") |
    [.message_id, .assistant_message.usage.input_tokens,
     .assistant_message.usage.output_tokens] | @tsv'
```

And when a number looks implausible, read it next to its neighbors: the
[schema page](/docs/schema/#points-worth-knowing) documents the
per-field semantics, and the raw provider fields are always preserved,
so the native reading is never lost.
