---
title: "Example: Comparing Harnesses"
description: Worked examples, from capture to side-by-sides of tool use, context, cost, failures, and delegation across harnesses.
icon: science
weight: 850
---

This page walks a real comparison end to end, then widens it. The first
part follows one task on two harnesses in detail; the second puts all
four supported harnesses side by side on three tasks. The same task ran
on Claude Code and Codex CLI:

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
| Events | 10 | 26 |
| Tool calls | 1 (`Write`, kind `edit`) | 3 (`exec`, kind `execute`) |
| File edit visible as | tool call | `patch_apply_end` telemetry |
| Tool time | 7 ms | 196 ms |
| Injected `system` events | 3 | 13 |
| Harness-origin user messages | 0 | 1 |
| `totals.input_tokens`, as recorded | 4 | 46,480 |
| Cache read tokens | 36,963 | 34,589 |
| Cache write tokens | 6,906 | 11,879 |
| `totals.total_prompt_tokens` (derived) | 43,873 | 46,480 |
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
    "cache_creation_input_tokens": 6906,
    "total_prompt_tokens": 43873
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
    "session_meta/base_instructions": 1,
    "task_complete": 1,
    "task_started": 1,
    "token_count": 4,
    "turn_context": 1,
    "world_state": 1
  },
  "totals": {
    "input_tokens": 46480,
    "output_tokens": 360,
    "cache_read_input_tokens": 34589,
    "cache_creation_input_tokens": 11879,
    "total_prompt_tokens": 46480
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
  Codex injected thirteen system events, including the system prompt,
  three developer messages and a world-state snapshot, plus one harness-origin user
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
  the per-field numbers keep their provider meanings; the schema now
  does the alignment for you in `totals.total_prompt_tokens`, which is
  exactly this arithmetic applied per convention. Codex's native
  `cache_write_input_tokens` (11,879 here) also lands in
  `cache_creation_input_tokens` now, so cache-write accounting no
  longer requires digging it out of the raw record with `--keep-raw`.
  [Comparing Token Counts](/docs/token-comparison/) generalizes this
  row into the full set of rules for cross-harness token analysis.
- **Behavior differences survive identical outcomes.** Same file, same
  `done`, and one harness took 2.5x the wall time and 3x the output
  tokens of the other on this tiny task. Which trade you prefer is your
  call; the transcripts are what make the trade visible.

## Four harnesses, three tasks

The same method scales sideways. The tables below come from one run of
`agentminutes drift probe --force --keep`, the maintainer tool that
drives every installed harness through a fixed task set and keeps the
transcripts (Antigravity CLI 1.2.11 on Gemini 3.8 Flash, Claude Code
2.1.274 on Claude Fable 5.1, Codex CLI 0.157.0 on GPT-6 Astra, GitHub
Copilot CLI 1.0.88 on Claude Sonnet 5). Every number is `stats` output
on those transcripts.

### The file task again

> Use your file-writing or patch tool to create a file named probe.txt
> in the current working directory containing exactly this text: drift
> probe. Do not use shell redirection or shell commands.

| | Antigravity | Claude Code | Codex | Copilot |
| --- | --- | --- | --- | --- |
| Events | 11 | 29 | 25 | 17 |
| Tool calls | 2 (`view_file`, `read`; `write_to_file`, `edit`) | 1 (`Write`, `edit`) | 1 (`exec`, `execute`) | 1 (`create`, `edit`) |
| File edit visible as | tool call | tool call | `FileChange` telemetry | tool call |
| Injected `system` events | 1 | 23 | 17 | 10 |
| Harness-origin user messages | 0 | 0 | 1 | 0 |
| `totals.total_prompt_tokens` | absent | 43,559 | 25,142 | absent |
| Output tokens | absent | 184 | 141 | absent |
| API calls | 3 | 2 | 2 | 2 |
| Tool time | 7.0 s | 596 ms | 73 ms | 10 ms |
| Wall time | 7.0 s | 6.5 s | 5.3 s | 4.5 s |

Three things the two-harness table could not show:

- **Two harnesses record no per-message usage at all**, so their
  `totals` are omitted rather than zero. Antigravity records nothing;
  Copilot CLI records session-cumulative per-model totals in a
  shutdown record, preserved as a `system` event. A cost column across
  all four needs those two read differently, as
  [Comparing Token Counts](/docs/token-comparison/) lays out.
- **The Codex edit is still telemetry.** On 0.157.0 the edit surfaces
  as an `item_completed` `FileChange` item rather than a
  `patch_apply_end` event, and `--promote codex:patch-apply` still
  recovers it as an `apply_patch` call of kind `edit`.
- **Antigravity's tool time is whole seconds.** Its records carry
  second-resolution timestamps, so the 7 seconds across its read and
  its write (this run it looked before writing) count second boundaries
  crossed, not the calls' own durations.

### A task that fails

> Do both of these with your tools and keep going after each one
> fails: run the shell command cat drift-probe-missing.txt (the file
> does not exist), then read the file
> /tmp/drift-probe-absent/nothing.txt with your file-reading tool.
> Then reply with exactly: done

All four replied `done`. What they recorded on the way:

| | Antigravity | Claude Code | Codex | Copilot |
| --- | --- | --- | --- | --- |
| Tool calls | 2 | 2 | 1 | 2 |
| `tool_errors` | 2 | 2 | 1 | 2 |
| Nonzero exit recorded as | "exited with code 1" in templated content, no error key | `is_error: true`, content `Exit code 1` plus stderr | success, with `exit_code: 1` in a JSON chunk of the output | `success: true` with `shellExecution.exitCode: 1` |
| Absent file recorded as | an `error` key on the step | `is_error: true` | (folded into the same script) | `error.code: "failure"` |

The `tool_errors` row agrees only because every adapter reads its
harness's own convention: two of the four harnesses call a failed
command a successful tool run. Codex reports one call because the
model ran both reads in one exec script, so per-call error rates are
not comparable either; count failed actions from the content when the
harness batches. See the `is_error` note on the
[schema page](/docs/schema/) for the fetch case, where the harnesses
genuinely disagree.

### A task that delegates

> Delegate this to a subagent using your agent-spawning tool (do not do
> it yourself): run the shell command echo drift-probe-subagent and
> report its exact output. When the subagent reports back, reply with
> exactly the output it reported.

| | Antigravity | Claude Code | Codex | Copilot |
| --- | --- | --- | --- | --- |
| Delegation call | `invoke_subagent` | `Agent` | `spawn_agent` + `wait_agent` | `task` |
| Parent transcript events | 10 | 29 | 32 | 32 |
| Subagent's work lives in | a sibling conversation | `<session>/subagents/agent-*.jsonl` | a sibling rollout file | the same file, 16 of the 32 events stamped with its `agent_id` |
| Subagent's own tool calls (in the parent) | 0 | 0 | 0 | 1 (`bash`) |
| Parent `total_prompt_tokens` | absent | 43,681 | 37,896 | absent |
| Subagent `total_prompt_tokens` | absent | 28,039 (its file) | 24,819 (its file) | absent |
| Models observed in the parent | 1 | 1 | 1 | 2 (`claude-sonnet-5`, `gpt-5.6-luna`) |

The same delegation produces three storage layouts, and a plain
`stats` on the parent reads differently on each: for Claude Code and
Codex it undercounts the task by a whole transcript (here about 40
percent of the prompt tokens), and for Copilot it already includes the
subagent's tool call and model. `stats --include-subagents` levels
this: it gathers the subagent transcripts where they exist, reports
the task aggregate (71,720 prompt tokens for Claude Code, 62,715 for
Codex), and splits every task per agent, so the parent-only and
task-scope numbers are both one field away on every harness.
[Subagents](/docs/subagents/#task-summaries) walks the output.

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
