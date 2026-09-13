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

## Docs site (site/)

Hugo + Lotus Docs site for agentminutes.dev, instantiated from
af-site-scaffold's template. Deploy with `site/build_and_sync` (rsync to
Dreamhost); llms.txt and per-page markdown are Hugo output formats,
regenerated on every build. Verify locally with
`hugo server -p 1721` + `afdocs check http://localhost:1721` (never port
1719/1720: Node's fetch blocks WHATWG bad-list ports and every check
reports "fetch failed"; `content-negotiation` passes only on the live
Apache site). The repo pre-commit hook runs `site/check_prose_style`
(Vale, DC style: em dashes and "not X, but Y" constructions are errors).
README.md is outside the hook's scope; lint it with
`vale --config site/.vale.ini README.md`.

New documentation pages (any new file under `site/content/`) need the
maintainer's review before they ship: write the page, lint and
build-check it, then stop and ask for a read. Do not commit or deploy
it until the maintainer has read the draft and said to proceed. This is
about new prose, whose framing the maintainer wants to see first;
mechanical refreshes of existing pages (re-captured command output,
version bumps, cross-link fixes) follow the normal commit flow.

Docs that move with the code:

- `content/docs/cli.md`: every example is captured real output. The
  convert/stats/sessions examples come from a minimal claude-code
  session (harness 2.1.236, `agentminutes_schema` "0.1.0"); the
  promotion example runs `harness/codex/testdata/rollout.jsonl` with
  and without `--promote codex:patch-apply`. When output shapes,
  SchemaVersion, or stats fields change, re-run the commands and
  re-capture rather than hand-editing numbers.
- `content/docs/schema.md`: the event-kind table mirrors `session/`
  types, and the schema revision appears inline. The "token fields
  carry provider semantics" bullet documents the per-provider
  `input_tokens` conventions, the codex cache_write mapping, and the
  derived `totals.total_prompt_tokens` (landed for issues #1/#2); it
  moves together with `example-comparison.md`'s token bullet (they
  document the same trap from two angles).
- `content/docs/token-comparison.md`: the cross-harness token field
  guide. It restates the per-provider `input_tokens` conventions, the
  `total_prompt_tokens` derivation, and the usage-source table (which
  names codex's `token_usage_record` handling), so it moves with
  `session/` usage semantics and with `schema.md`'s token bullet; its
  numbers reference `example-comparison.md`'s captured run rather than
  carrying captures of its own.
- `content/docs/example-comparison.md`: a captured same-task run
  (claude-code 2.1.212 vs codex 0.146.0), including token arithmetic
  tied to current provider usage semantics. The stats blocks were
  refreshed when `total_prompt_tokens` and the codex cache_write
  mapping landed: the codex block is re-run output over the surviving
  source transcript (`~/.codex/sessions/2026/08/02/rollout-*-4eb8a22861a9.jsonl`);
  the claude-code source transcript is gone, so a future stats-shape
  change there means re-capturing from a fresh run.
- `content/docs/discovery.md`: the resumed-turns walkthrough is a
  captured two-turn claude-code run; stable unless resume semantics
  change.
- `content/docs/harnesses.md`: support table mirrors the registry and
  README table (the alphabetical-lists rule covers all three).
- `data/landing.yaml` hero badge pins the release version;
  `assets/images/terminal-hero.svg` (hand-edited) and
  `sharing-image.html` bake in command output. After editing
  sharing-image.html, run `site/generate_sharing_image` (headless
  Chrome) to re-render `static/sharing.png`.
- `content/docs/cli.md` promotions prose mirrors
  `plans/telemetry-promotion.md` behavior and the exported transform
  names; keep them aligned when promotions are added.
