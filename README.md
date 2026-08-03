# agentminutes

Meeting minutes for your agents: parse native agent harness session logs
into one unified, comparable event schema.

Agent harnesses record everything that happens in a session (messages,
tool calls, tool results, token usage) in their native transcript files,
and every harness invents its own format. Those transcripts are ground
truth for how an agent actually behaved: which tools it chose, what it
retrieved, what it saw back, what it spent. agentminutes parses them into
a single event schema so you can analyze and compare sessions across
harnesses, in Go or from the command line.

It is the companion to
[agentsummons](https://github.com/agent-ecosystem/agentsummons), which
invokes the harnesses headlessly: **agentsummons convenes the meeting,
agentminutes takes the minutes.**

## Status

Early development, pre-release. The normalized schema is versioned
(currently `0.1.0`) and appears in every output record; expect it to
evolve until 1.0.

| Harness | Status | Native format |
| --- | --- | --- |
| Antigravity CLI | Supported | `~/.gemini/antigravity-cli/brain/<id>/.system_generated/logs/transcript_full.jsonl` |
| Claude Code | Supported | `~/.claude/projects/<project>/*.jsonl` |
| Codex CLI | Supported | `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` |
| Gemini CLI (classic) | Not planned | Retired for individual users June 2026; Antigravity is its successor |

Adapters are validated against real transcripts with a mechanical
line-accounting check: every source line becomes an event, a counted
skip, or an error.

## Install

```bash
brew install agent-ecosystem/tap/agentminutes
# or
npm install -g agentminutes
# or
pip install agentminutes
# or
go install github.com/agent-ecosystem/agentminutes/cmd/agentminutes@latest
```

As a Go library: `go get github.com/agent-ecosystem/agentminutes`.
Prebuilt static binaries are on the
[releases page](https://github.com/agent-ecosystem/agentminutes/releases).

## Quick start

```bash
# Parse a transcript into one normalized JSON session record
# (harness auto-detected):
agentminutes convert ~/.claude/projects/my-project/some-session.jsonl

# Find the transcripts you care about without parsing everything:
agentminutes sessions --cwd ~/runs/exp-42 | xargs -n1 agentminutes convert

# Summarize a session's behavior: tool mix, bytes retrieved,
# latency, tokens, final answer:
agentminutes stats session.jsonl
```

As a library:

```go
s, err := agentminutes.Parse(f, harness.ClaudeCode, harness.Options{})
if err != nil {
    return err
}
fmt.Println(s.Meta.HarnessVersion, len(s.Events), s.Totals.OutputTokens)
```

## Documentation

Full documentation is available at
**[agentminutes.dev](https://agentminutes.dev)**:

- [Quickstart](https://agentminutes.dev/docs/quickstart/): install and
  parse your first transcript
- [Use Cases](https://agentminutes.dev/docs/use-cases/): why compare
  transcripts across harnesses
- [CLI](https://agentminutes.dev/docs/cli/): convert, sessions, detect,
  stats, and drift, with real output for each
- [Go Library](https://agentminutes.dev/docs/library/): whole-file
  parsing, streaming, transforms, and the ACP projection
- [Session Discovery](https://agentminutes.dev/docs/discovery/): finding
  transcripts by cwd, session ID, and time window
- [The Schema](https://agentminutes.dev/docs/schema/): the event
  vocabulary and the guarantees behind it
- [Harness Support](https://agentminutes.dev/docs/harnesses/):
  validation coverage and format drift
- [Design Notes](https://agentminutes.dev/docs/design/): loud failure,
  post-hoc parsing, streaming, the explicit registry
- [Example: Comparing Two Harnesses](https://agentminutes.dev/docs/example-comparison/):
  a worked comparison from capture to side-by-side

## Contributing

Adapters live under [`harness/`](harness/), one package per harness. To
add one, follow [DEVELOPMENT.md](DEVELOPMENT.md): it covers the full
process from generating ground-truth transcripts through the standard
test suite.

## License

[MIT](LICENSE)
