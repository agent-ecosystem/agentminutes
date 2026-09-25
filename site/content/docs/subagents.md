---
title: Subagents
description: How subagent transcripts are stored, how they tie back to the parent session, and how to analyze a whole task.
icon: account_tree
weight: 550
---

Harnesses can delegate work to subagents: separate conversations with
their own context windows, tool loops, and token usage. Claude Code
records each subagent as its own transcript file next to the parent's.
This is easy to miss, and missing it distorts every downstream number.
If you parse only the file named by the session ID, you see the
delegation happen, and none of the delegated work: the tool calls, the
files read, and roughly all of the subagents' token usage are in files
you never opened.

All the harnesses can spawn subagents, and their recordings differ in
exactly the ways that bite an analysis. This page walks the Claude Code
mechanics in depth (where the files live, the three ways they tie back
to the parent, and how to analyze a task as a whole), then covers what
Codex, Antigravity, and Copilot CLI do instead. Everything here was validated against
live spawn probes on the versions in
[Harness Support](/docs/harnesses/).

## What the parent transcript shows

In the parent session, a delegation is an ordinary `tool_call` named
`Agent`, whose input carries the delegated `prompt` and a short
`description`. The matching `tool_result` contains the text the
subagent returned, and its `enrichment` carries the harness sidecar
data, including the key that names the subagent:

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

That is all the parent records. The subagent's own conversation, tool
calls, and usage are elsewhere.

## Where the subagent files live

Claude Code stores subagent transcripts under the parent session's
directory:

```
~/.claude/projects/<project>/
  <session-id>.jsonl                     # the parent transcript
  <session-id>/subagents/
    agent-a69749826ce0a9ed8.jsonl        # one transcript per subagent
    agent-a69749826ce0a9ed8.meta.json    # sidecar; discovery skips it with a reason
```

The `agent-<id>.jsonl` files are complete transcripts in the same
format as the parent and parse identically.

## Tying subagents to the parent

Three joins, all mechanical:

1. **The `agentId` key.** The parent's `Agent` tool result enrichment
   carries `agentId`; it matches the subagent's filename
   (`agent-<agentId>.jsonl`) and the `subagent_id` in the subagent's
   own session meta. This joins a specific delegation to a specific
   transcript.
2. **Session meta.** A subagent transcript's `meta` shares the parent's
   `session_id` and adds `subagent_id` plus `is_subagent: true`, so
   grouping a mixed pile of converted sessions by task is a group-by on
   `session_id`.
3. **Discovery.** [Session discovery](/docs/discovery/) treats the
   parent and its subagent files as one session: `sessions
   --session-id` lists all of them, and the Go `Locate` ref carries
   `SubagentPaths` alongside the parent `Path`. A subagent file whose
   parent transcript is missing still surfaces as a standalone ref
   rather than being dropped.

```sh
$ agentminutes sessions --harness claude-code --session-id 4d51ce48-0e3d-4321-82f5-435769bc5ab4
~/.claude/projects/my-project/4d51ce48-0e3d-4321-82f5-435769bc5ab4.jsonl
~/.claude/projects/my-project/4d51ce48-0e3d-4321-82f5-435769bc5ab4/subagents/agent-a32569d82c684ca51.jsonl
~/.claude/projects/my-project/4d51ce48-0e3d-4321-82f5-435769bc5ab4/subagents/agent-a69749826ce0a9ed8.jsonl
~/.claude/projects/my-project/4d51ce48-0e3d-4321-82f5-435769bc5ab4/subagents/agent-a7578b0c629cb97e7.jsonl
~/.claude/projects/my-project/4d51ce48-0e3d-4321-82f5-435769bc5ab4/subagents/agent-a9966d78dab862c6f.jsonl
agentminutes: 1 session, 0 filtered out, 0 skipped files, 0 errors
```

## Reading a subagent transcript

Everything on the [schema page](/docs/schema/) applies unchanged. One
seat assignment deserves attention: the subagent's first
`user_message` is the delegated prompt, and it carries
`origin: "human"`, because inside that conversation the parent agent
occupies the user seat. When "how many prompts did the human write"
matters to your analysis, filter on `meta.is_subagent` first rather
than trusting `origin` across a whole task's files:

```sh
agentminutes sessions --harness claude-code --session-id "$SESSION" |
  while read -r t; do agentminutes convert "$t"; done |
  jq -s 'map(select(.meta.is_subagent | not)
      | [.events[] | select(.kind == "user_message"
          and .user_message.origin == "human")] | length)
    | add'
```

On the four-subagent session used throughout this page, the filtered
count is 13. Dropping the `is_subagent` filter reports 17: the four
delegated prompts read as human-origin messages, one per subagent, and
inflate the count by exactly the number of delegations.

## Accounting for a whole task

Each transcript's `totals` cover only its own API context; parent
totals exclude subagent usage entirely. In a real four-subagent
session, the parent-only reading missed 37 percent of the task's
prompt volume and half of its output tokens.
[Comparing Token Counts](/docs/token-comparison/#subagent-usage-lives-in-separate-transcripts)
works through that example and the aggregation recipe; the short
version is one discovery call and a sum:

```sh
agentminutes sessions --harness claude-code --session-id "$SESSION" |
  while read -r t; do
    agentminutes stats "$t" | jq '.totals.total_prompt_tokens // 0'
  done | jq -s add
```

The same scope decision applies to every other metric: tool-call
counts, bytes retrieved, and wall time all read differently at
conversation scope (parent only) and task scope (parent plus
subagents). Decide which question you are asking before you aggregate.

## Codex

Codex (0.154.0, multi-agent enabled by default) records each subagent
as its **own rollout file** in the same dated tree as every other
session. The identity design is friendly to analysis:

- A subagent rollout's `session_id` is the **root thread's id at every
  spawn depth** (validated to depth 2), so its converted
  `meta.session_id` matches the parent's, and grouping a task is the
  same `session_id` match as Claude Code. The subagent's own thread id
  appears as `meta.subagent_id` with `is_subagent: true`, and its
  position in the delegation tree is in the native `agent_path`
  (`/root/relay/pong`).
- In the parent, a delegation is a `spawn_agent` tool call (its
  `message` argument is encrypted, like reasoning) paired with a
  result, and the harness telemetry (`SubAgentActivity` system events)
  names the spawned thread's id. Messages between agents appear as
  `agent_message` system events carrying author and recipient agent
  paths.
- Each subagent rollout has its own `totals`, so the whole-task
  aggregation recipe above works unchanged: discover by the parent's
  session ID and sum. One discovery caveat: rollout filenames carry
  each thread's own id, so `sessions --harness codex --session-id
  <parent>` resolves only the parent's file directly; a scan
  (`sessions --session-id <parent>` across roots, or `--root` with
  time filters) is what gathers the whole task.

## Antigravity

Antigravity (1.2.2) spawns subagents as ordinary **sibling
conversations** under its brain directory. The parent records an
`invoke_subagent` tool call answered by a step whose content embeds
the created child's conversation id and transcript path; the child's
transcript looks exactly like a normal session (its first user input
is the delegated prompt in the standard wrapper), and it reports back
with a `send_message` tool call whose arguments name the parent
conversation id as recipient.

Nothing structural marks the child as a subagent, so `is_subagent`
stays unset for antigravity, and tying files together is a content
join on those embedded ids. Both directions of the join are
extractable from converted events. Parent to children: the
subagent-creation step pairs as the `invoke_subagent` call's tool
result, and its text embeds each created child's conversation id:

```sh
agentminutes convert --format jsonl "$PARENT" |
  jq -r 'select(.kind == "tool_result"
      and .tool_result.tool_name == "invoke_subagent")
    | .tool_result.content[0].text
    | capture("\"conversationId\":\\s*\"(?<id>[0-9a-f-]+)\"").id' |
  while read -r child; do
    agentminutes sessions --harness antigravity --session-id "$child"
  done
```

Child to parent: the `send_message` call's input names the parent
conversation id as its recipient:

```sh
agentminutes convert --format jsonl "$CHILD" |
  jq -r 'select(.kind == "tool_call"
      and .tool_call.name == "send_message")
    | .tool_call.input.Recipient'
```

Both recipes are verified against a real spawn: the first prints the
child's conversation id and resolves it to its transcript path, and
the second prints the parent's id from inside the child. Token
accounting is unaffected either way: antigravity transcripts record
no usage, so subagent or not, its sessions have no `totals`.

## GitHub Copilot CLI

Copilot CLI (1.0.88) records subagent conversations **inside the parent
transcript** rather than in files of their own. A delegation is a
`tool_call` named `task` (a general subagent whose prompt, name, and
`agent_type` are in the input) or `search_code_subagent` (a read-only
search agent on a dedicated model with a constrained toolset of
`file_search`, `grep_search`, and `read_file`). What follows in the
stream is the subagent's own conversation: its prompt as a
`user_message` with origin `harness` (the parent agent wrote it, not
the human), its `assistant_message`, `thinking`, `tool_call`, and
`tool_result` events, and its turn boundaries, every one stamped with
the subagent's `agent_id`. The parent's `tool_result` for the
delegation carries the subagent's final text, and can land before or
after the subagent's last events.

`system` events bracket each subagent: `subagent.started` (the parent
tool call id, agent name and type, resolved model), `subagent.configured`,
`subagent.selected` (the search agent's tool list), and
`subagent.completed`. For a `task` subagent, `subagent.completed` also
records `totalToolCalls`, `totalTokens`, and `durationMs`, the only
per-subagent usage the format carries. The join is `subagent.started`:
its `agent_id` is the value on the subagent's events, and its
`toolCallId` is the parent's `tool_call`.

Delegation comes in more shapes than the sync case, all recorded the
same way: two `task` calls in one message run as two agents whose
records interleave (tool call ids keep the pairing straight); a
background agent (`mode: "background"`) returns at once and is driven
afterwards through `list_agents`, `read_agent`, and `write_agent`,
whose follow-up turns appear under the same `agent_id` without a new
lifecycle bracket; a `general-purpose` agent can delegate again, and
the nested agent's `subagent.started` names its parent agent in
`parentId`; and a custom agent from `.github/agents` runs as
`agent_type: "<name>"` with a `subagent.selected` listing its tools.

Two consequences for analysis:

- **Parent-only numbers need an `agent_id` filter.** Unlike the other
  harnesses, parsing the one transcript already includes the delegated
  work: `stats` counts the subagent's tool calls in `tool_calls`, and
  its model appears in `models`. Keep events whose `agent_id` is empty
  for the parent's own behavior.
- **Nothing to gather.** One session record covers the task, so the
  discovery step the other harnesses need does not apply; `Meta`
  never marks a Copilot transcript as a subagent's.

