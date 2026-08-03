---
title: Session Discovery
description: Finding transcripts by cwd, session ID, and time window.
icon: travel_explore
weight: 500
---

Harnesses write transcripts to global, harness-owned locations
(`~/.claude/projects`, `~/.codex/sessions`,
`~/.gemini/antigravity-cli/brain`). Discovery scans those roots, reads
each transcript's identity cheaply (a header read via the real parser,
never a second format), and filters by the keys harnesses actually
record.

## From the CLI

The `sessions` command is the discovery entry point; see
[CLI](/docs/cli/#agentminutes-sessions) for its filters and examples.

## From Go

`agentminutes.Scan` enumerates every harness's default root.
`agentminutes.Locate` resolves a known session ID to its transcript
path(s), which is the capture-time primitive for runners that archive
transcripts right after a headless invocation:

```go
ref, err := agentminutes.Locate(harness.ClaudeCode, sessionID)
if err != nil {
    return err // wraps os.ErrNotExist when the session has no transcript
}
archive(ref.Path)                    // the main transcript
for _, p := range ref.SubagentPaths { // Claude Code agent-*.jsonl files
    archive(p)
}
```

For non-default roots, use `agentminutes.LocatorFor(id).Scan(root, opts)`.

## Refs survive resumed turns

A located ref stays valid across resumed turns: every supported harness
appends resumes to the same transcript rather than forking a new session
(validated on the versions listed in
[Harness Support](/docs/harnesses/)). Multi-turn runners can re-`Locate`
the same ID after each turn to pick up the grown transcript and any new
subagent files.

Here is what that looks like end to end, driving Claude Code through two
turns with [agentsummons](https://agentsummons.dev/docs/multi-turn/) and
locating between them. Turn 1 presets the session identity, and the
transcript is findable as soon as the turn completes:

```sh
$ SESSION=84cffcb8-161f-4921-8564-ed889068ddb3
$ agentsummons run --harness claude-code --session-id "$SESSION" \
    -p "Reply with exactly one word: ping" --auto-approve
ping
$ agentminutes sessions --harness claude-code --session-id "$SESSION"
~/.claude/projects/-tmp-demo/84cffcb8-161f-4921-8564-ed889068ddb3.jsonl
$ wc -l < ~/.claude/projects/-tmp-demo/84cffcb8-*.jsonl
      10
```

Turn 2 resumes the same session. Locating again returns the same path,
and the transcript has grown in place:

```sh
$ agentsummons run --harness claude-code --resume "$SESSION" \
    -p "Reply with exactly one word: pong" --auto-approve
pong
$ agentminutes sessions --harness claude-code --session-id "$SESSION"
~/.claude/projects/-tmp-demo/84cffcb8-161f-4921-8564-ed889068ddb3.jsonl
$ wc -l < ~/.claude/projects/-tmp-demo/84cffcb8-*.jsonl
      18
```

Parsing that one transcript yields one session record covering both
turns:

```sh
$ agentminutes stats ~/.claude/projects/-tmp-demo/84cffcb8-*.jsonl | jq '{user_messages, event_counts, final_answer}'
{
  "user_messages": 2,
  "event_counts": {
    "assistant_message": 2,
    "session_meta": 1,
    "system": 4,
    "thinking": 2,
    "user_message": 2
  },
  "final_answer": "pong"
}
```

The archiving consequence: a runner that grabs the ref after turn 1 can
keep re-reading the same path after every later turn, and an archive
taken after the final turn contains the whole conversation. There is no
second file to forget.

## The accounting discipline

Scans obey the same accounting discipline as parses, lifted from
per-line to per-file: everything under a root is a yielded ref, a
reported skip (`ScanOptions.OnSkip`), or a `*harness.ScanError`, which,
unlike a parse error, does not end the scan.

Discovery never parses beyond each transcript's head, and it never
invents identity except where the layout is the documented source. An
Antigravity session ID is its conversation directory name; the format
records none in-band. From agy 1.1.8, a headless run with
`--output-format json` receives the conversation ID in its result
envelope, so capture-time runners need time-window discovery only in
text mode or on older releases.
