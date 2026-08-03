---
title: "Example: Comparing Two Harnesses"
description: A worked example, from capture to a side-by-side of tool use, context, and cost.
icon: science
weight: 850
---

This page walks a real comparison end to end. The same task ran on Claude
Code and Codex CLI:

> Create a file named hello.txt containing exactly the word hello, then
> reply done.

Both harnesses created the file and replied `done`. Everything below is
about what the transcripts reveal happened between the prompt and that
identical answer, and every number on this page comes from the two real
sessions.

## Capture the sessions

agentminutes needs only transcripts; how they were produced doesn't
matter. There are two equally good ways to get comparable sessions:

**Drive the runs headlessly.** The companion
[agentsummons](https://agentsummons.dev) invokes any supported harness
with one command shape, which is the easy way to hold the task constant:

```sh
agentsummons run --harness claude-code --session-id "$SESSION" \
  --workdir /tmp/task-cc -p "$TASK" --allowed-tools Write --auto-approve

agentsummons run --harness codex --workdir /tmp/task-cx \
  -p "$TASK" --auto-approve
```

**Or use sessions you already have.** Interactive sessions land in the
same harness-owned stores and parse identically, so
`sessions --cwd`/`--since` finds them without any runner in the loop
(see [Session Discovery](/docs/discovery/)).

## Locate the transcripts

The Claude Code run preset its session ID, so it resolves directly. The
Codex run didn't, so the working directory finds it:

```sh
$ agentminutes sessions --harness claude-code --session-id 36547337-416d-48c8-bcd9-7046cd8fc70b
~/.claude/projects/-private-tmp-task-cc/36547337-416d-48c8-bcd9-7046cd8fc70b.jsonl

$ agentminutes sessions --harness codex --cwd /private/tmp/task-cx
~/.codex/sessions/2026/08/02/rollout-2026-08-02T20-21-34-019fc4ff-51d0-77e0-a11a-4eb8a22861a9.jsonl
```

One capture-time trap: harnesses record the symlink-resolved working
directory, so on macOS a session run in `/tmp/task-cx` matches
`--cwd /private/tmp/task-cx` and a query for `/tmp/task-cx` finds
nothing.

## The side-by-side

`agentminutes stats` on each transcript, compared:

| | Claude Code | Codex CLI |
| --- | --- | --- |
| Final answer | `done` | `done` |
| Events | 10 | 25 |
| Tool calls | 1 (`Write`, kind `edit`) | 3 (`exec`, kind `execute`) |
| File edit visible as | tool call | `patch_apply_end` telemetry |
| Tool time | 7 ms | 196 ms |
| Injected `system` events | 3 | 12 |
| Harness-origin user messages | 0 | 1 |
| `totals.input_tokens`, as recorded | 4 | 46,480 |
| Cache read tokens | 36,963 | 34,589 |
| Total prompt tokens processed | 43,873 | 46,480 |
| Output tokens | 104 | 360 |
| API calls (usage snapshots) | 2 | 4 |
| Wall time | 4.0 s | 10.0 s |

The trimmed `stats` fields behind the interesting rows:

```json
// claude-code
{
  "tool_calls_by_name": { "Write": 1 },
  "tool_calls_by_kind": { "edit": 1 },
  "system_by_subtype": {
    "attachment/agent_listing_delta": 1,
    "attachment/deferred_tools_delta": 1,
    "attachment/skill_listing": 1
  },
  "totals": {
    "input_tokens": 4,
    "output_tokens": 104,
    "cache_read_input_tokens": 36963,
    "cache_creation_input_tokens": 6906
  }
}
```

```json
// codex
{
  "tool_calls_by_name": { "exec": 3 },
  "tool_calls_by_kind": { "execute": 3 },
  "system_by_subtype": {
    "message/developer": 3,
    "patch_apply_end": 1,
    "task_complete": 1,
    "task_started": 1,
    "token_count": 4,
    "turn_context": 1,
    "world_state": 1
  },
  "totals": {
    "input_tokens": 46480,
    "output_tokens": 360,
    "cache_read_input_tokens": 34589
  }
}
```

## What the comparison shows

- **The same action, logged differently.** Claude Code wrote the file
  with one `Write` tool call, classified `edit`. Codex ran three `exec`
  commands and recorded the actual file edit only as `patch_apply_end`
  telemetry. Un-promoted, a "file edits per task" metric scores this 1
  to 0; with `--promote codex:patch-apply`, Codex reports
  `apply_patch: 1`, kind `edit`, and the metric becomes fair. This is
  the situation
  [telemetry promotions](/docs/cli/#telemetry-promotions) exist for.
- **Context injection has a per-harness profile.** Claude Code injected
  three attachments (skill listing, agent listing, deferred tools).
  Codex injected twelve system events, including three developer
  messages and a world-state snapshot, plus one harness-origin user
  message that the `origin` marker keeps separate from the human's
  prompt. Whatever "the model saw" means for your analysis, it differed
  before the first token of the reply.
- **Token fields carry provider semantics, and the 4 vs 46,480 row is
  the trap that proves it.** At face value Codex looks four orders of
  magnitude more expensive. It isn't: OpenAI-style accounting reports
  `input_tokens` as the total prompt, with `cached_input_tokens` as a
  subset of it, while Anthropic-style accounting reports `input_tokens`
  as only the uncached remainder, with cache reads and writes in
  separate fields. Aligned, the runs are nearly identical: Claude Code
  processed 4 + 6,906 + 36,963 = 43,873 total prompt tokens against
  Codex's 46,480, both overwhelmingly served from cache. Adapters
  preserve what the harness recorded rather than reinterpreting it, so
  cross-harness cost comparisons must sum the cache-aware fields, never
  compare `input_tokens` directly. (The native Codex record also
  carries `cache_write_input_tokens: 11879`; `provenance` plus
  `--keep-raw` is how you get at fields like that.)
- **Behavior differences survive identical outcomes.** Same file, same
  `done`, and one harness took 2.5x the wall time and 3x the output
  tokens of the other on this tiny task. Which trade you prefer is your
  call; the transcripts are what make the trade visible.

## Scaling it up

Two sessions fit in a table by hand. For a real experiment, emit one
summary per session and aggregate with whatever you already use:

```sh
for t in runs/*.jsonl; do
  agentminutes stats --promote codex:patch-apply "$t" -o "summaries/$(basename "$t" .jsonl).json"
done
```

Each summary is one JSON document with the fields above, ready for `jq`,
a dataframe, or a dashboard. The
[Use Cases](/docs/use-cases/) page covers where this kind of comparison
pays off; the [Go Library](/docs/library/) page covers doing the same
analysis in-process.
