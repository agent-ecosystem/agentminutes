---
title: CLI
description: The convert, sessions, detect, stats, and drift commands.
icon: terminal
weight: 300
---

## agentminutes convert

To parse a transcript into a normalized session record, use `convert`
(harness auto-detected):

```sh
agentminutes convert ~/.claude/projects/my-project/some-session.jsonl
```

The default output is one JSON document: session metadata, the ordered
events, token totals, and a report accounting for everything that did not
become an event. `totals` is omitted entirely when the transcript records
no usage (Antigravity), so a measured zero is never conflated with "not
recorded".

This is the output for a minimal two-message session, with the middle of
the event list trimmed:

```json
{
  "agentminutes_schema": "0.2.0",
  "meta": {
    "harness": "claude-code",
    "harness_version": "2.1.236",
    "session_id": "18870970-e221-4cc3-8ea9-39bcbffa2ad7",
    "cwd": "/Users/me/my-project",
    "git_branch": "main"
  },
  "events": [
    {
      "kind": "session_meta",
      "timestamp": "2026-09-13T16:42:41.925Z",
      "provenance": { "line": 3, "end_line": 3 },
      "session_meta": {
        "harness": "claude-code",
        "harness_version": "2.1.236",
        "session_id": "18870970-e221-4cc3-8ea9-39bcbffa2ad7",
        "cwd": "/Users/me/my-project",
        "git_branch": "main"
      }
    },
    {
      "kind": "user_message",
      "timestamp": "2026-09-13T16:42:41.925Z",
      "id": "8d62c944-f259-40e1-ae99-6497fcc580be",
      "provenance": { "line": 3, "end_line": 3 },
      "user_message": {
        "origin": "human",
        "content": [
          { "kind": "text", "text": "Reply with exactly one word: pong" }
        ]
      }
    }
  ],
  "totals": {
    "input_tokens": 2,
    "output_tokens": 4,
    "cache_read_input_tokens": 16015,
    "cache_creation_input_tokens": 6792,
    "total_prompt_tokens": 22809
  },
  "report": {
    "skipped_records": {
      "ai-title": 1,
      "atis-latch": 1,
      "last-prompt": 1,
      "queue-operation": 2
    }
  }
}
```

Note the `report`: the five skipped records are Claude Code UI
bookkeeping with no model-visible content, and they're counted rather
than dropped. Between events, skips, and errors, every line of the
transcript is accounted for.

To stream events as JSONL instead (one event per line on stdout, with the
same per-type skip accounting summarized on stderr):

```sh
agentminutes convert --format jsonl session.jsonl | jq -r 'select(.kind == "tool_call") | .tool_call.name'
```

For example, listing the same session's event kinds:

```sh
$ agentminutes convert --format jsonl session.jsonl | jq -r .kind
session_meta
user_message
system
system
system
system
assistant_message
agentminutes: 7 events, 5 skipped records (ai-title 1, atis-latch 1, last-prompt 1, queue-operation 2)
```

The last line is the stderr summary; it stays out of your pipe.

### Useful flags

- `--harness claude-code` skips auto-detection (also on `stats`).
- `--harness-version 1.1.1` (also on `stats`) records the harness version
  for formats that don't record one themselves (Antigravity). Metadata
  only; it never changes how the transcript is parsed, and a version the
  transcript records wins.
- `--permissive` (also on `stats`) preserves unclassifiable records as
  `unknown` events instead of failing the parse.
- `--keep-raw` retains the verbatim native records in each event's
  provenance.
- `--max-payload-bytes N` replaces tool-result payloads larger than N
  with size-and-digest placeholders.
- `--text-form delivered` makes a `system` event's `text` the text as
  the harness records delivering it to the model, wrapper included,
  where the transcript records that form: Claude Code's rendered
  `<system-reminder>` blocks (2.1.267+) and Copilot's `<skill-context>`
  wrapper around a loaded skill. The default, `bare`, is the record's
  own text without the framing, which is what phrase tracing and
  cross-harness comparison want; `delivered` is for experiments that
  must reproduce what the model saw byte for byte. Records with no
  recorded delivered form keep their bare text either way.
- `--promote codex:web-search` / `--promote codex:patch-apply` (also on
  `stats`) opt into a telemetry promotion; see
  [Telemetry promotions](#telemetry-promotions) below.
- `-o out.json` (also on `stats`) writes to a file; passing `-` as the
  transcript reads stdin.

## agentminutes sessions

To find transcripts without parsing everything, use `sessions`:

```sh
# Every session that ran in a given working directory, ready to pipe:
agentminutes sessions --cwd ~/runs/exp-42 | xargs -n1 agentminutes convert

# One known session, resolved directly (Claude Code subagent transcripts
# are included in the output; forgetting them is the classic archiving bug):
agentminutes sessions --harness claude-code --session-id 0199c9a3-...

# Full metadata instead of paths, bounded by time:
agentminutes sessions --since 2026-07-19 --format jsonl
```

The default output is one transcript path per line on stdout, with an
accounting summary on stderr:

```sh
$ agentminutes sessions --harness claude-code --session-id 18870970-e221-4cc3-8ea9-39bcbffa2ad7
~/.claude/projects/my-project/18870970-e221-4cc3-8ea9-39bcbffa2ad7.jsonl
agentminutes: 1 session, 0 filtered out, 0 skipped files, 0 errors
```

Filters: `--harness`, `--root` (explicit root, requires `--harness`),
`--cwd`, `--session-id`, `--since`/`--until` (RFC 3339 or bare dates). A
stderr summary accounts for what didn't match and why; sessions with no
recorded cwd (Antigravity records none) never match `--cwd` and are
counted in the summary rather than left unexplained. Per-file scan errors go to stderr
and set exit status 1 without aborting the scan. See
[Session Discovery](/docs/discovery/) for the discovery model.

## agentminutes detect

To identify a transcript's harness, use `detect`:

```sh
$ agentminutes detect session.jsonl
claude-code	certain
```

## agentminutes stats

To summarize a session's behavior (tool mix, bytes retrieved, latency,
tokens, observed models, final answer), use `stats`:

```sh
agentminutes stats session.jsonl
```

This is the complete summary for the same minimal session shown under
`convert`:

```json
{
  "events": 7,
  "event_counts": {
    "assistant_message": 1,
    "session_meta": 1,
    "system": 4,
    "user_message": 1
  },
  "user_messages": 1,
  "tool_calls": 0,
  "result_bytes": 0,
  "system_by_subtype": {
    "attachment/agent_listing_delta": 1,
    "attachment/deferred_tools_delta": 1,
    "attachment/skill_listing": 1,
    "attachment/total_tokens_reminder": 1
  },
  "models": [
    "claude-fable-5"
  ],
  "totals": {
    "input_tokens": 2,
    "output_tokens": 4,
    "cache_read_input_tokens": 16015,
    "cache_creation_input_tokens": 6792,
    "total_prompt_tokens": 22809
  },
  "start_time": "2026-09-13T16:42:41.925Z",
  "end_time": "2026-09-13T16:42:43.603Z",
  "wall_time_ms": 1678,
  "final_answer": "pong"
}
```

Even this trivial session shows the shape of the analysis: the harness
injected four `system` attachments (skill listings, tool deltas, and a token budget reminder)
before the model said a word, and cache reads dwarf fresh input tokens.
The token fields keep each provider's own semantics, so
`totals.total_prompt_tokens` is the derived, cross-harness comparable
prompt size; see the
[schema page](/docs/schema/#points-worth-knowing) for the trap it
resolves.
`models` lists the models observed on assistant messages in
first-observed order, so more than one entry means the serving model
changed mid-session.

### Task summaries

A session that delegated to subagents is more than one transcript on
most harnesses. `--include-subagents` treats the given transcript as
the parent, gathers its subagent transcripts through the harness's own
discovery rules (under `--root`, or the default root), and prints a
task summary: each transcript's `stats` on its own, an aggregate
`task` in the same shape, and a `by_agent` split with the parent under
the empty key. `join` names how the subagents were found (`layout`,
`session_id`, `content`, or `inline`), and `skipped`, when present,
lists what the gather could not include, with a reason, so a summary
that is short says so. Nothing is counted twice, and a harness that
records subagents inline (Copilot CLI) contributes one transcript whose
split comes from the events.

```sh
agentminutes stats --include-subagents ~/.claude/projects/<project>/<session-id>.jsonl
```

See [Subagents](/docs/subagents/#task-summaries) for the output
walked through on a real session and what each harness records
underneath.

### Telemetry promotions

The summary's `system_by_subtype` counts surface actions a harness
records only as telemetry. Codex 0.144 logs URL fetches solely as
`web_search_end` events, and 0.144.6 records file edits solely as
`patch_apply_end`, so they appear there rather than as tool calls.
Codex 0.149 drops those subtypes and records the same actions only as
`item_completed` records (a `FileChange` item for the edit, a
web-search `Extension` item for the fetch); the same promotions cover
both vocabularies, so the flags below work unchanged across versions.

This matters whenever the question you're asking is about tool behavior.
Take a cross-harness comparison like "how many file edits did each
harness make on this task?" (see
[Use Cases](/docs/use-cases/#compare-harnesses-on-the-same-task)):
Claude Code records edits as tool calls, so its edits land in
`tool_calls_by_name`, but a Codex session whose edits live in telemetry
reports zero. The comparison skews on a logging difference: both
harnesses edited files, and only one recorded the edits as tool calls. The same applies to
retrieval metrics built on fetch counts and bytes, and to
`Session.ToolInteractions()`, which only joins `tool_call`/`tool_result`
pairs and can't see actions that never became either.

The rule of thumb: promote when your analysis counts, times, or compares
tool calls; leave telemetry as telemetry when you're studying what the
harness records natively. To opt in:

```sh
agentminutes stats --promote codex:web-search --promote codex:patch-apply rollout.jsonl
```

Here is the difference on a Codex session whose transcript records one
file edit only as telemetry. Without promotion, the edit hides in
`system_by_subtype` and tool metrics see one call:

```json
{
  "tool_calls": 1,
  "tool_calls_by_name": { "exec": 1 },
  "tool_calls_by_kind": { "execute": 1 },
  "system_by_subtype": {
    "patch_apply_end": 1,
    "task_complete": 1,
    "task_started": 1
  }
}
```

With `--promote codex:patch-apply`, the same transcript reports the edit
as a tool call, classified `edit`, and `patch_apply_end` leaves the
system counts:

```json
{
  "tool_calls": 2,
  "tool_calls_by_name": { "apply_patch": 1, "exec": 1 },
  "tool_calls_by_kind": { "edit": 1, "execute": 1 },
  "system_by_subtype": {
    "task_complete": 1,
    "task_started": 1
  }
}
```

The synthesized events are visible in `convert` output, pointing back at
the telemetry record they came from:

```json
{
  "kind": "tool_call",
  "timestamp": "2026-07-19T22:00:04Z",
  "provenance": { "line": 11, "end_line": 11 },
  "tool_call": {
    "tool_call_id": "exec-x1",
    "name": "apply_patch",
    "kind": "edit",
    "input": { "/tmp/exp/hello.txt": { "type": "add", "content": "hello\n" } },
    "promoted_from": "event_msg/patch_apply_end"
  }
}
```

A matching `tool_result` is synthesized alongside it, carrying the
telemetry record's stdout and success status in its `enrichment`.

Promotions are never on by default, because harness versions that also
record the action natively would double-count. Synthesized events carry
`promoted_from` so they stay auditable.

## agentminutes drift

If a parse fails or the output looks wrong, check whether the
transcript's format has drifted past what this build was validated
against (free; nothing is invoked):

```sh
$ agentminutes drift scan session.jsonl
session.jsonl: claude-code
  drift: record type "assistant" has new key "responseMeta"
```

A clean scan reports what it checked, including baseline record types
the transcript happened not to contain:

```sh
$ agentminutes drift scan session.jsonl
session.jsonl: claude-code
  info: baseline record types not exercised: [bridge-session file-history-delta file-history-snapshot frame-link mode permission-mode pr-link system]
  clean: matches the claude-code baseline
```

A drift finding usually means the harness updated its log format; check
for an agentminutes update or file an issue with the scan output. Exit
codes: 0 clean, 1 drift.
