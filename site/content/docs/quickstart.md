---
title: Quickstart
description: Install agentminutes and parse your first transcript.
icon: rocket_launch
weight: 100
---

## Install

macOS users can `brew` install:

```sh
brew install agent-ecosystem/tap/agentminutes
```

For other install options, including npm, PyPI, go install, and prebuilt
binaries, refer to [Installation](/docs/installation/).

## Convert a transcript

To parse a native transcript into a normalized session record, use the
`convert` command (the harness is auto-detected):

```sh
agentminutes convert ~/.claude/projects/my-project/some-session.jsonl
```

The default output is one JSON document: session metadata, the ordered
events, token totals, and a report accounting for everything that did not
become an event.

## Find your transcripts

Harnesses write transcripts to global, harness-owned locations. To find
the sessions you care about without parsing everything, use the
`sessions` command:

```sh
agentminutes sessions --cwd ~/runs/exp-42 | xargs -n1 agentminutes convert
```

This scans every supported harness's default root, reads each
transcript's identity cheaply, and filters by the keys harnesses actually
record. See [Session Discovery](/docs/discovery/).

## Pick up subagent transcripts

A session that delegated work to subagents is more than one file:
Claude Code and Codex both write each subagent conversation as its own
transcript, with its own events and its own token totals.
Discovery treats them as one session, so a session-ID lookup lists
every file the task touched, ready to convert together:

```sh
agentminutes sessions --harness claude-code --session-id "$SESSION" |
  xargs -n1 agentminutes convert
```

A parent transcript's totals cover only its own context, so skipping
the subagent files undercounts the task. See
[Subagents](/docs/subagents/) for how the files tie together and how
to aggregate a whole task.

## Summarize a session

To see a session's behavior at a glance (tool mix, bytes retrieved,
latency, tokens, observed models, final answer), use the `stats` command:

```sh
agentminutes stats session.jsonl
```

For the full command reference, refer to [CLI](/docs/cli/).
