# agentminutes

Go library + CLI parsing native agent harness session logs (Antigravity CLI, Claude Code, Codex CLI) into a unified event schema. Read `README.md` for the schema contract and `DEVELOPMENT.md` before touching adapters; the empirical format inventories under `plans/` are the ground truth for each harness's quirks.

## Commands

```bash
go test ./... -count=1        # unit tests (fixture-based, hermetic)
golangci-lint run             # lint + gofumpt (CI-enforced)
GOOS=windows go build ./...   # CI also tests on windows-latest

# Validate adapters against real transcripts on this machine (strict parse
# + per-line accounting, plus the per-file discovery accounting from
# TestLocalScan; run after any adapter or locator change):
AGENTMINUTES_LOCAL_TRANSCRIPTS=~/.claude/projects go test ./harness/claudecode/ -run 'TestLocal' -v
AGENTMINUTES_LOCAL_CODEX_TRANSCRIPTS=~/.codex/sessions go test ./harness/codex/ -run 'TestLocal' -v
AGENTMINUTES_LOCAL_ANTIGRAVITY_TRANSCRIPTS=~/.gemini/antigravity-cli/brain go test ./harness/antigravity/ -run 'TestLocal' -v
# ACP projection loss report over real transcripts:
AGENTMINUTES_LOCAL_TRANSCRIPTS=~/.claude/projects go test ./acp/ -run TestLocalLossReport -v
```

## Non-negotiables

- Never silently drop input: every line becomes an event, a counted skip, or a loud `*harness.ParseError` (harness, version, line). Line-accounting tests enforce this.
- The JSON encoding of `session.Session`/`session.Event` is the cross-language contract; schema changes need `schema:"acp|otel|ext"` tags (reflection-test enforced), `Event.Validate` updates, and a `SchemaVersion` review.
- Harness lists (constants, registry, flag help, README table) stay alphabetical.
- Keep fixtures synthetic (no vendor system prompts, no personal data) and LF-only (`.gitattributes` handles this; don't fight it).
- Harness formats drift fast. Before adapter work, regenerate ground-truth transcripts per `DEVELOPMENT.md` step 0 and update the inventory doc in `plans/`. `agentminutes drift probe` automates the check (version-gated against `harness.LastValidated`; spends real tokens, Antigravity quota especially), and `drift scan` diffs existing transcripts against the embedded vocabulary baselines for free. Baselines (`internal/driftprobe/baselines/`), `LastValidated`, and the inventories move together; tests enforce the pairing.
- Adapters translate with total accounting and never make representational judgment calls. Opt-in policy lives in `session.Transform` functions exported by adapter packages (e.g. `codex.PromoteWebSearch`), never applied by default, always marking synthesized events via `promoted_from`. See `plans/telemetry-promotion.md`.

## Layout

- `session/` schema + `Accumulator` + `Stats()` + `Transform`; `harness/` adapter contract + `Locator` discovery contract + `LastValidated`; `harness/<name>/` one package per harness (parser + `locate.go`); `acp/` ACP projection + loss report; `internal/parseutil/` shared adapter helpers; `internal/locatetest/` per-file scan accounting invariant; `internal/driftprobe/` drift devtool engine + embedded baselines (headless invocation delegates to the agentsummons library — flag knowledge lives there); `cmd/agentminutes/` CLI (`convert`, `detect`, `drift`, `sessions`, `stats`); facade in root `agentminutes.go`; `wrappers/` npm + PyPI wrapper packages (checked-in sources with tests against a fake binary; `npm/scripts/build-packages.mjs` and `pypi/build_wheels.py` assemble publishable artifacts from goreleaser archives, and the release workflow publishes them per tag via registry trusted publishing — structure mirrors agentsummons' `wrappers/`, keep them aligned).
- `plans/` holds design/status docs that are not user documentation: format inventories, registration plan, next steps.
