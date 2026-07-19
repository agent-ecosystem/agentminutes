# Drift probe: design

Status: designed and built (`internal/driftprobe/`,
`cmd/agentminutes/drift.go`; usage documented in DEVELOPMENT.md "The drift
devtool"). This is the on-demand drift-check tool anticipated by the
format-drift notes in `next-steps.md` and the "Harness format versioning"
section of DEVELOPMENT.md. Two decisions made during implementation, on
top of the design below:

- Baselines record **validated observed vocabulary** only. Shapes
  implemented from documentation but never observed locally (e.g. Codex
  `function_call`) are deliberately absent: if one appears in the wild,
  the drift flag is a true positive prompting re-validation of a code
  path that has never been tested against reality.
- Claude Code MCP tool names (`mcp__*`) are collapsed to a wildcard in
  the tool-name discriminator: they are user configuration, not harness
  vocabulary, and would otherwise flag false drift on every user's scan.

## Purpose

An on-demand devtool that answers "has any installed harness's transcript
format drifted past what the adapters were validated against?" by invoking
the harnesses headlessly against fixed probe tasks, strict-parsing the fresh
transcripts, and diffing their observed vocabulary against a committed
baseline. Secondary audience: a user who gets unexpected parser output can
run the zero-cost scan mode against their own transcript to see whether
drift is the cause.

Non-goals, by decision:

- **No cron / scheduled runs.** Drift arrives with harness upgrades, not
  with time; probes cost real tokens (Antigravity especially). The version
  gate below is the failsafe against unmitigated spend, not a scheduler.
- **No passive corpus sweep.** Only Claude Code sees regular local use;
  Codex/Antigravity transcripts exist locally only around cross-harness
  experiments. The env-gated `TestLocalTranscripts` remain available but are
  not part of this tool.
- **No CI integration.** All three harnesses need real auth (Antigravity's
  is interactive OAuth requiring a TTY) and probe runs spend quota.

## CLI surface

A `drift` subcommand of the existing `agentminutes` binary (one install,
discoverable by end users), with three verbs:

```bash
agentminutes drift probe [--harness all|<id>,...] [--force] [--keep] [--timeout 5m]
agentminutes drift scan <transcript>...
agentminutes drift baseline <transcript>... [-o <file>]
```

- **probe**: the full pipeline (version gate, headless probe runs, shape
  assertions, strict parse, vocabulary diff). Costs tokens.
- **scan**: vocabulary-diff existing transcripts against the baseline, no
  harness invocation. Zero cost; auto-detects the harness per file. This is
  the mode to point users at when they report unexpected parser output.
- **baseline**: regenerate a baseline JSON from transcripts (used once per
  harness initially, then whenever drift is reconciled). Writes the file;
  the developer reviews the diff and bumps `harness.LastValidated` and the
  inventory doc alongside it.

Visibility is asymmetric (the Tailscale `debug` pattern): `scan` is the
user-facing diagnostic and appears in help; `probe` and `baseline` are
maintainer tooling, registered with cobra `Hidden: true` so they ship in
the one binary but stay out of `--help`. They are documented in
DEVELOPMENT.md and mentioned in the `drift` command's long help, and
`probe`'s own help states plainly that it invokes harness binaries with
permission-skipping flags and spends tokens. Rationale: single-binary
distribution is the Go norm and `scan` must work for any installed copy,
while a verb that spends the user's quota should not be the first thing
casual `--help` browsing surfaces.

Flags: `--force` re-probes even when the installed version equals
`LastValidated`; `--keep` retains the probe workdir and copies of the fresh
transcripts for inspection (default: temp dir, removed); `--timeout` bounds
each probe invocation.

## Pipeline (probe mode)

### 1. Version gate

For each target harness, run its version command (`claude --version`,
`codex --version`, `agy --version`), extract the version, and compare to
`harness.LastValidated` with the existing `versionNewer` semantics:

- equal → "up to date, skipping (use --force to probe anyway)", exit clean
- newer → proceed to probes
- older or binary missing → report and skip (older installs are not drift)

This step is free and is the reason the tool can be invoked casually.

### 2. Probe runs

Each probe is (name, prompt, expected shapes). Probes run sequentially per
harness in a fresh `os.MkdirTemp` workdir (never a real project; prompts
are fixed and harmless). Invocations follow the step-0 recipes:

| Harness | Invocation sketch |
|---|---|
| antigravity | `agy --dangerously-skip-permissions --add-dir <workdir> -p "<prompt>"` (prompt flag last; `agy` ignores cwd without `--add-dir`) |
| claude-code | `claude --dangerously-skip-permissions -p "<prompt>"` with `cmd.Dir = workdir` |
| codex | `codex exec` with auto-approval per the codex inventory, `cmd.Dir = workdir` |

The probe set exercises every schema shape the adapters map, in unified
schema terms so the assertion code is harness-agnostic:

| Probe | Prompt intent | Must appear in parsed events |
|---|---|---|
| qa | "Reply with exactly: pong" | `assistant_message` with text content |
| shell | run `echo drift-probe`, report output | `tool_call` kind `execute` + paired `tool_result` |
| file | create `probe.txt` containing a fixed string | `tool_call` kind `edit` (or write-mapped kind) + result |
| search | glob `notes/*.txt`, grep a needle string in a seeded workdir (claude-code only; other harnesses search via the shell) | `tool_call`s named `Glob` and `Grep`, each with a result (name-exact: ToolSearch is also kind `search`, so a kind count passes vacuously) |
| fetch | fetch a stable URL, state its title | `tool_call` kind `fetch` + result |
| multi | two tasks in one prompt (shell + file) | ≥2 `tool_call`s in one session |

Implementation delta (first-consumer reconciliation): probes gained
optional fields — `Harnesses` (restrict a probe to the harnesses where the
tool family is dedicated-tool vocabulary), `Files` (seed the workdir),
`AllowedTools` (typed tool restriction, mapped to the harness flag by
agentsummons; headless claude-code 2.1.197 does not register Glob/Grep at
all unless they are named in its tool restriction), and `ExtraArgs` (the
verbatim-flag escape hatch for anything the typed fields don't model).

### 3. Transcript location

Snapshot "probe started at T"; after the run, discover transcripts through
the harness's own `Locator` (the same discovery production uses, so
locator-excluded sidecars like Antigravity's trimmed `transcript.jsonl`
can never masquerade as probe transcripts) and keep those with mtime > T.
A fresh file the locator reports a `ScanError` for is still a candidate —
an unidentifiable fresh transcript is exactly what a probe must look at.
Zero matches is an infra error. This avoids parsing harness stdout for
session IDs, which differs per harness and per version (the thing we're
testing).

### 4. Shape assertions (vacuity guard)

Strict-parse the transcript (`Permissive: false`) and check the probe's
expected shapes against the parsed events. A probe whose expected shape is
missing is **inconclusive, not drift**: the model may simply not have
called the tool. Inconclusive probes are retried once with a more insistent
prompt; still missing → reported as inconclusive with its own exit code so
a green run always means "the shapes were actually exercised and parsed."

Line accounting runs here too: every non-blank transcript line must be
covered by an event's provenance range or a skip callback (same invariant
as the adapter tests; the coverage check moves into a shared helper so the
tool and the tests use one implementation).

### 5. Vocabulary diff

Extract the transcript's observed vocabulary and diff against the
committed baseline. The vocabulary is:

- top-level record types, and the key set per type (depth 2 key paths)
- per-harness **discriminator values**, where each format hides its churn:
  - codex: `payload.type` values within `event_msg` and `response_item`
  - claude-code: record `type` values and `message.content[].type` block types
  - antigravity: `type` values and `source` values

Diff semantics:

- new record type, new discriminator value → **drift** (this is exactly
  the additive churn strict parsing tolerates silently, e.g. unknown codex
  telemetry subtypes mapped to `system` events)
- new key on a known type → **drift** (additive field)
- baseline entry absent from fresh transcripts → **info** ("not exercised
  by this probe set"), never a failure; probes cannot exercise everything
  (compaction, resume, subagents)

### 6. Report and exit codes

Human-readable report per harness: version transition (validated → 
installed), per-probe outcome, parse result, vocabulary diff grouped by
severity. Exit codes: `0` clean or skipped by the version gate; `1` drift
detected (parse failure or vocabulary drift); `2` inconclusive probes only;
`3` execution error (binary missing, timeout, transcript not found).

On a reconciled drift: rerun with fresh transcripts through
`drift baseline`, update `harness.LastValidated`, update the inventory doc,
extend fixtures if shapes changed. The report ends with this checklist.

## Baseline format and location

One JSON per harness, embedded in the binary via `go:embed` (so an
installed `agentminutes` can scan without the repo) and overridable with a
flag for development:

```json
{
  "harness": "codex",
  "generated_from_version": "0.144.1",
  "record_types": {
    "event_msg": {
      "keys": ["payload", "payload.type", "timestamp", "type"],
      "discriminators": {"payload.type": ["agent_message", "token_count", "..."]}
    }
  }
}
```

`generated_from_version` is informational; `harness.LastValidated` in code
stays the single authoritative coverage record (a test should assert the
two agree). Initial baselines are generated from the local real-transcript
corpus plus fixtures, reviewed by hand against the inventory docs.

## Code layout

- `cmd/agentminutes/drift.go`: thin cobra wiring (three verbs).
- `internal/driftprobe/`: probe tables, harness invocation configs,
  transcript location, vocabulary extraction/diff, report. Internal so the
  library API doesn't grow; the CLI is the interface.
- `internal/driftprobe/baselines/<harness>.json`: embedded baselines.
- Shared line-accounting helper extracted to `internal/parseutil` (or a
  small `internal/coverage`) and adopted by the adapter tests.
- Probe invocation configs live in a table, alphabetical like all harness
  lists; adding a harness adds a table row, probes and assertions are
  shared.

Testing: vocabulary extraction and diffing are pure and get unit tests
against fixtures (including a doctored "drifted" fixture with a new record
type, new key, and new discriminator value). Probe orchestration is tested
with a fake harness binary (a shell script that writes a canned transcript)
behind the same invocation table; real-harness runs stay manual/on-demand
by design.

## Cost and safety notes

- Antigravity probes spend real quota (Google AI Pro); the version gate
  plus explicit `--harness antigravity` selection keeps this deliberate.
- Probes run with permission-skipping flags, so containment comes from the
  fixed harmless prompts and the disposable workdir. Prompts must never
  ask for anything outside the workdir; the fetch probe uses a stable,
  boring URL (example.com).
- `--keep` plus the raw transcripts is the debugging path; transcripts may
  contain vendor system prompts, so kept copies stay out of the repo (the
  fixture-hygiene rule applies: anything committed must be synthetic).
