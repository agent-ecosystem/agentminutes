---
title: Subagents
description: Task summaries across subagent transcripts, and how each harness records delegation underneath.
icon: account_tree
weight: 550
---

Harnesses can delegate work to subagents: separate conversations with
their own context windows, tool loops, and token usage. Three of the
four supported harnesses write each subagent conversation somewhere
other than the parent transcript, and the fourth writes it inside the
parent. Either way, a naive reading of one file gets the task wrong: it
either misses the delegated work entirely (the tool calls, the files
read, and roughly all of the subagents' token usage are in files you
never opened) or it counts the subagent's work as the parent's.

agentminutes does that accounting for you: a **task summary** is the
summary of a whole task, every transcript the harness wrote for it
gathered and aggregated without double counting. This page covers task
summaries first, then what each harness records underneath, for when
you need to know how much to trust a grouping.

## Task summaries

`stats --include-subagents` produces a task summary. It treats a
transcript as a task's parent,
gathers the subagent transcripts the harness wrote for it through the
harness's own discovery rules, and reports three things in one
document:

- Every transcript summarized on its own
- An aggregate in the same shape as a single `stats` summary
- A per-agent split.

```sh
agentminutes stats --include-subagents ~/.claude/projects/<project>/<session-id>.jsonl
```

Trimmed output for a real one-delegation session (Claude Code 2.1.274):

```json
{
  "agentminutes_schema": "0.1.0",
  "harness": "claude-code",
  "session_id": "f6f82cf7-af5c-45d5-9931-d07ea3cf0d92",
  "join": "layout",
  "transcripts": [
    {
      "path": "~/.claude/projects/<project>/<session-id>.jsonl",
      "stats": {
        "tool_calls": 1,
        "tool_calls_by_name": { "Agent": 1 },
        "totals": {
          "input_tokens": 34,
          "output_tokens": 199,
          "cache_read_input_tokens": 35232,
          "cache_creation_input_tokens": 8466,
          "total_prompt_tokens": 43732
        },
        "final_answer": "drift-probe-subagent"
      }
    },
    {
      "path": "~/.claude/projects/<project>/<session-id>/subagents/agent-ac5dc0a183ec99c2b.jsonl",
      "subagent_id": "ac5dc0a183ec99c2b",
      "is_subagent": true,
      "stats": {
        "tool_calls": 1,
        "tool_calls_by_name": { "Bash": 1 },
        "totals": {
          "input_tokens": 34,
          "output_tokens": 151,
          "cache_read_input_tokens": 13236,
          "cache_creation_input_tokens": 14984,
          "total_prompt_tokens": 28254
        }
      }
    }
  ],
  "task": {
    "tool_calls": 2,
    "tool_calls_by_name": { "Agent": 1, "Bash": 1 },
    "totals": {
      "input_tokens": 68,
      "output_tokens": 350,
      "cache_read_input_tokens": 48468,
      "cache_creation_input_tokens": 23450,
      "total_prompt_tokens": 71986
    },
    "final_answer": "drift-probe-subagent",
    "by_agent": {
      "": { "tool_calls": 1, "totals": { "total_prompt_tokens": 43732, "output_tokens": 199 } },
      "ac5dc0a183ec99c2b": { "tool_calls": 1, "totals": { "total_prompt_tokens": 28254, "output_tokens": 151 } }
    }
  }
}
```

How to read it:

- **`transcripts`** is the parent first, then each subagent transcript,
  each with its own complete `stats`. Conversation-scope questions
  ("what did the parent conversation cost") read the first entry.
- **`task`** is the aggregate: counts and per-name maps summed, models
  unioned, the time span covering every transcript, the parent's final
  answer, and `totals` summed with `total_prompt_tokens` re-derived
  for the harness's convention, so the aggregate is as comparable
  across harnesses as a single summary is. Task-scope questions
  ("what did this task cost") read here.
- **`task.by_agent`** splits the task per agent: the parent under the
  empty key, each subagent under its id. It is the same view whether
  the harness wrote separate files or interleaved the subagent into the
  parent transcript.
- **`join`** names how the subagent transcripts were found, which is
  how much to trust the grouping: `layout` (Claude Code, files in a
  position derived from the parent's path), `session_id` (Codex, sibling
  files that record the parent's id in-band), `content` (Antigravity,
  children named in the parent's tool results: a heuristic), or
  `inline` (Copilot CLI, one file, split by `agent_id`).
- **`skipped`**, when present, lists what the gather could not include
  (a candidate transcript it could not read, a child conversation no
  longer in the store) with a reason, so a task summary that is short
  says so rather than looking complete.

Nothing is counted twice. Each transcript contributes its own numbers,
a subagent's transcript is never also the parent's, and a harness that
records subagents inline contributes one transcript whose per-agent
split comes from the events. Nested delegation is followed: a
subagent's own subagents are gathered into the same task.

Two things a task summary does not do. It does not change what a plain
`stats` on one file reports, because conversation scope is a
legitimate question, and it does not invent usage: a harness that
records none (Antigravity) or none per message (Copilot CLI) has no
`totals` at task scope either, so absence stays distinguishable from
zero. `--root` points discovery at a non-default transcript root, and
`--promote` applies to every transcript in the task.

In Go, `agentminutes.Task` returns the same summary as a `TaskStats`
value, `harness.Locator.Gather` exposes the discovery step on its own,
and `session.SumStats` does the aggregation for summaries you already
hold; see [Go Library](/docs/library/#task-scope-summaries).

## What the parent transcript shows

In the parent session, a delegation is an ordinary `tool_call` (named
`Agent` on Claude Code, `spawn_agent` on Codex, `invoke_subagent` on
Antigravity, `task` on Copilot CLI) whose input carries the delegated
prompt. The matching `tool_result` contains the text the subagent
returned, and its `enrichment` carries the harness sidecar data,
including whatever key names the subagent:

```json
{
  "kind": "tool_result",
  "tool_result": {
    "name": "Agent",
    "content": [ { "kind": "text", "text": "…the subagent's report…" } ],
    "enrichment": {
      "agentId": "a69749826ce0a9ed8",
      "resolvedModel": "claude-fable-5",
      "status": "completed",
      "prompt": "…the delegated prompt…"
    }
  }
}
```

On Claude Code a custom agent (defined with `--agents`) also puts
`agentType`, `totalTokens`, `totalToolUseCount`, and `toolStats` in
that sidecar, and on 2.1.274 a `SendMessage` call reaches a running
agent or another session, whose reply arrives as a `user_message` with
origin `harness` (natively an `isMeta` record with a peer `origin` and
`handback: true`). The subagent's conversation and tool calls are still
elsewhere.

## Reading a subagent transcript

Everything on the [schema page](/docs/schema/) applies unchanged to a
subagent transcript. One seat assignment deserves attention: on the
harnesses that write separate files, the subagent's first
`user_message` is the delegated prompt and carries `origin: "human"`,
because inside that conversation the parent agent occupies the user
seat. When "how many prompts did the human write" matters, filter on
`meta.is_subagent` (or, at task scope, read `by_agent` and count only
the parent's). Copilot CLI is the exception: its subagent prompts are
inline and already carry origin `harness`.

## How each harness records delegation

A task summary hides these differences; this is what it is hiding, so
a `join` value or a surprising number can be traced.

| | Claude Code | Codex | Antigravity | Copilot CLI |
| --- | --- | --- | --- | --- |
| Subagent conversation | its own file under `<session-id>/subagents/` | its own rollout file in the dated tree | a sibling conversation directory | inside the parent transcript |
| Join | layout | `session_id` recorded in-band | conversation ids embedded in `invoke_subagent` results | `agent_id` on every event |
| Subagent identity | `meta.subagent_id`, `is_subagent: true` | `meta.subagent_id`, `is_subagent: true`, `session_id` is the root's at every depth | none in-band; the conversation directory name | the `agent_id` (a fresh id, or the parent's tool call id for the search agent) |
| Nesting | subagents cannot delegate | validated to depth 2 | followed by the same content join | `subagent.started` names the parent agent |
| Usage at task scope | summed across files | summed across files | none recorded | none per message |

Two Claude Code details worth knowing. Discovery treats the parent and
its subagent files as one session: `sessions --session-id` lists all of
them, and a subagent file whose parent transcript is missing still
surfaces as a standalone ref rather than being dropped. And the
`.meta.json` sidecars beside subagent transcripts are skipped with a
counted reason.

Two Codex details. Rollout filenames carry each thread's own id, so
`sessions --harness codex --session-id <parent>` resolves the parent's
file alone; `--include-subagents` and `Gather` do the sibling scan for
you. The scan is by session id under the root, so a parent transcript
copied elsewhere still gathers the root's siblings. Messages between agents appear as `agent_message` system events
carrying author and recipient agent paths, and the harness telemetry
(`SubAgentActivity` system events) names each spawned thread.

One Antigravity detail. Nothing structural marks a child conversation,
so the join reads the child's conversation id out of the
`invoke_subagent` result text; a child that is no longer in the store
is skipped, and `join: "content"` is the signal to treat the grouping
as a heuristic. The child reports back with a `send_message` call whose
input names the parent conversation id as recipient.

And Copilot CLI: a delegation's whole conversation follows the `task`
or `search_code_subagent` call in the same stream, every event stamped
with the subagent's `agent_id`, bracketed by `subagent.started` and
`subagent.completed` system events (the latter carrying `totalTokens`
for a `task` agent). Parallel agents interleave, a background agent's
follow-up turns (`write_agent`) appear under the same `agent_id`
without a new bracket, and a plain `stats` on the file already includes
the subagent's tool calls and model, which is exactly what `by_agent`
separates.

Everything on this page was validated against live delegation probes on
the versions in [Harness Support](/docs/harnesses/), and the drift
probe's `subagent` task re-checks the join and the sums on every run.
