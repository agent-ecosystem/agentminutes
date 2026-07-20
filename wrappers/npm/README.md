# agentminutes

Parses native agent harness session logs (Antigravity CLI, Claude Code,
Codex CLI) into a unified event schema, so tool calls, results, and
messages are comparable across harnesses.

This package wraps the prebuilt Go binary. The matching platform binary
(darwin, linux, or win32 on x64 or arm64) is installed automatically via
an optional dependency; nothing is downloaded at install time beyond the
npm packages themselves.

Windows x64 is temporarily unavailable on npm (a registry naming issue is
being worked out with npm support); use the
[PyPI package](https://pypi.org/project/agentminutes/) or a
[release binary](https://github.com/agent-ecosystem/agentminutes/releases)
with `AGENTMINUTES_BINARY` in the meantime. Windows arm64 is unaffected.

```bash
npm install -g agentminutes
agentminutes --version

# or run without installing
npx agentminutes stats session.jsonl
```

Also available via Homebrew (`brew install agent-ecosystem/tap/agentminutes`)
and `go install github.com/agent-ecosystem/agentminutes/cmd/agentminutes@latest`.

- Repository, docs, and schema contract: <https://github.com/agent-ecosystem/agentminutes>
- Project site: <https://agentminutes.dev>
