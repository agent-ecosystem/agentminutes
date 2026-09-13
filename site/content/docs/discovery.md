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

## Subagent files belong to their session

A Claude Code session that delegated work to subagents spans several
files: the parent transcript, plus one transcript per subagent under
`<session-id>/subagents/` next to it. Discovery treats them as one
session rather than several. A session-ID lookup returns the parent
path and every subagent path together (the Go ref's `SubagentPaths`,
shown above, is the same grouping):

```sh
$ agentminutes sessions --harness claude-code --session-id 4d51ce48-0e3d-4321-82f5-435769bc5ab4
~/.claude/projects/my-project/4d51ce48-0e3d-4321-82f5-435769bc5ab4.jsonl
~/.claude/projects/my-project/4d51ce48-0e3d-4321-82f5-435769bc5ab4/subagents/agent-a32569d82c684ca51.jsonl
~/.claude/projects/my-project/4d51ce48-0e3d-4321-82f5-435769bc5ab4/subagents/agent-a69749826ce0a9ed8.jsonl
~/.claude/projects/my-project/4d51ce48-0e3d-4321-82f5-435769bc5ab4/subagents/agent-a7578b0c629cb97e7.jsonl
~/.claude/projects/my-project/4d51ce48-0e3d-4321-82f5-435769bc5ab4/subagents/agent-a9966d78dab862c6f.jsonl
agentminutes: 1 session, 0 filtered out, 0 skipped files, 0 errors
```

Two edge behaviors are deliberate. A subagent transcript whose parent
file is missing still surfaces, as a standalone ref, so partial
archives stay discoverable. And the `.meta.json` sidecars next to the
subagent transcripts are excluded with a counted skip reason, under
the same accounting discipline described below.

Codex subagents are sibling rollout files rather than nested ones, and
their filenames carry each thread's own id while the in-band
`session_id` records the root thread's. Consequences for discovery: a
scan groups a task by `session_id` (the filter also matches a
subagent's own thread id), while `Locate` resolves exactly one thread's
file, so `--harness codex --session-id <parent>` returns the parent
alone and a scan is what gathers the whole task. Antigravity subagent
conversations are structurally ordinary sessions; nothing in the
layout or transcript marks them, so discovery cannot group them
(the linkage is embedded in step content). For how the files tie back
to the parent on each harness and how to analyze a whole task, see
[Subagents](/docs/subagents/).

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
taken after the final turn contains the whole conversation. Resuming
never creates a second file to forget; delegation does, and the same
ref lists those subagent files too (see above).

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
