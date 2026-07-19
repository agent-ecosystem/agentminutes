# agentminutes

Parses native agent harness session logs (Antigravity CLI, Claude Code,
Codex CLI) into a unified event schema, so tool calls, results, and
messages are comparable across harnesses.

This package wraps the prebuilt Go binary: each platform wheel (macOS,
Linux, or Windows on x86_64 or arm64) bundles the matching binary and
exposes it as the `agentminutes` console command. Nothing is downloaded
at install time.

```bash
pip install agentminutes
agentminutes --version
```

Also available via Homebrew (`brew install agent-ecosystem/tap/agentminutes`)
and `go install github.com/agent-ecosystem/agentminutes/cmd/agentminutes@latest`.

- Repository, docs, and schema contract: <https://github.com/agent-ecosystem/agentminutes>
- Project site: <https://agentminutes.dev>
