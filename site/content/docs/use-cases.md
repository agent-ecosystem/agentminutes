---
title: Use Cases
description: Why parse and compare agent transcripts across harnesses?
icon: lightbulb
weight: 150
---

Harness transcripts record what an agent actually did: which tools it
chose, what it retrieved, what it saw back, what it spent. That makes
them the evidence base for any question about agent behavior. The catch
is that every harness writes a different format, so any analysis
written against one harness's logs stops at that harness. Normalizing
to one schema is what turns "read three formats" into "answer the
question once." You might want to use agentminutes to:

## Compare harnesses on the same task

Run the same prompt through Claude Code, Antigravity CLI, and Codex CLI
(its companion [agentsummons](https://agentsummons.dev) exists to do
exactly this), and the normalized transcripts show where the runs
diverged: which tools each harness chose, how many calls it made, what
it injected into context, and what the whole thing cost.

This is how the
[Agent Skill Implementation](https://agentskillimplementation.com)
benchmark works: the same probe skills run on every harness, and every
finding about platform behavior cites a transcript rather than trusting
the model's self-reporting about its own context. The transcript is what
makes a claim like "this harness never loaded the skill" checkable.

## Measure what the model actually saw

`tool_result.content` is the post-pipeline content the model received,
and the `fetch` enrichment records the raw bytes retrieved. Comparing
the two measures a harness's retrieval pipeline directly. For a
summarizing pipeline like Claude Code's WebFetch, that ratio is the
pipeline's compression rate on your content.

If you publish documentation, this answers a question that is otherwise
guesswork: when an agent fetched your page, how much of it survived the
trip to the model?

## Account for tokens and cost

Token usage fields follow OTel GenAI conventions, and the
`assistant_message` accounting anchor guarantees totals are derivable
even when a harness splits one API message across many records. That
makes "what did this task cost on each harness?" a query instead of a
spreadsheet reconstruction. The `models` list also exposes mid-session
serving-model changes, which would otherwise skew cost comparisons with
no visible signal.

## Debug and audit a run

When a session goes sideways, the transcript is the flight recorder.
The normalized event stream shows injected context (`system` events),
errors, retries, parallel tool execution, and orphaned results in
stream order, and every event's `provenance` points at the exact lines
in the native transcript that produced it. Sessions that ended in a
timeout still parse: partial evidence beats no evidence when you're
archiving failures.

## Watch behavior change across releases

Harnesses ship weekly and their behavior shifts: different tool
preferences, different context injection, different retry patterns. Run
the same task before and after an upgrade and diff the `stats`
summaries. Because the schema is stable across harness versions, the
comparison survives the upgrade even when the native log format
doesn't. Format changes are their own signal; `drift scan` catches
those separately. See [CLI](/docs/cli/#agentminutes-drift).

## Feed downstream tooling

The JSON encoding of a session is a versioned, cross-language contract.
JSONL event streams pipe into `jq`, dashboards, or eval pipelines
without Go in the loop, and `acp.Project` maps sessions onto the
[Agent Client Protocol](https://agentclientprotocol.com/) vocabulary
for tooling that already speaks ACP. If you're building agent
observability, the parsing layer is done.

## When one harness is enough

The cross-harness comparison is the headline, but the accounting
discipline pays off on a single harness too: every line becomes an
event, a counted skip, or an error, so behavioral metrics never rest on
records that were dropped without notice. If you only ever parse Claude
Code transcripts, you still get the flight recorder, the cost
accounting, and the loud failure when a new release changes the format
under you.
