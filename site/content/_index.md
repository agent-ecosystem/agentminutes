---
title: agentminutes
description: Parse native agent harness session logs (Antigravity CLI, Claude Code, Codex CLI) into one unified, comparable event schema.
---

Meeting minutes for your agents. Agent harnesses record everything that
happens in a session (messages, tool calls, tool results, token usage) in
their native transcript files, and every harness invents its own format.
Those transcripts are ground truth for how an agent actually behaved.
agentminutes parses them into a single event schema so you can analyze and
compare sessions across harnesses, in Go or from the command line.

It is the companion to
[agentsummons](https://agentsummons.dev), which invokes the harnesses
headlessly: agentsummons convenes the meeting, agentminutes takes the
minutes.
