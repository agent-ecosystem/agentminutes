# agentminutes: name registration plan

Goal: claim the `agentminutes` name across the namespaces that matter for a Go-core library with future npm/PyPI wrapper packages.

Availability was verified at decision time (all registries below were free, zero GitHub repos with the exact name, no product using the name). Availability can change at any time, so do the registrations promptly and re-verify each one just before claiming (commands at the bottom).

## Status

| Namespace | Status | Priority |
| --- | --- | --- |
| agentminutes.dev | Registered | Done |
| GitHub repo (and optionally org) | Not yet claimed | High: this is the Go module path |
| npm | Not yet claimed | High |
| PyPI | Not yet claimed | High |
| Homebrew | Cannot pre-reserve; comes with first release | N/A |
| crates.io / RubyGems | Skipping (see notes) | Low |

## 1. GitHub

**DECIDED: the module path is `github.com/agent-ecosystem/agentminutes`** (same org as skill-validator, matching the local workspace layout). It is baked into `go.mod` and every import path in the working tree; treat it as permanent. The recommendation below to defensively claim the `agentminutes` org still stands, since it remains the most confusable namespace.

Original decision framing, kept for the record: Go bakes the owner into the module path (`github.com/dacharyc/agentminutes` vs `github.com/agentminutes/agentminutes`), and changing it later breaks every importer. GitHub redirects renamed/transferred repos for git operations, but the Go module proxy treats the path as the identity, so treat this choice as permanent.

- Option A, personal: create `github.com/dacharyc/agentminutes`.
- Option B, org: claim the `agentminutes` org (it was unclaimed at decision time), create `agentminutes/agentminutes`. The org route reads better long-term if the parsing lib grows companion repos (wrappers, schema, fixtures), which is plausible given the separate-wrapper plan.

Recommendation: claim the org either way, even if the repo starts under the personal account. Org creation is free and prevents someone else from squatting the most confusable namespace.

Go itself needs no registration: pkg.go.dev indexes the module automatically the first time anyone fetches it after a tagged release.

## 2. npm

npm's dispute policy treats empty placeholder packages as squatting, and truly empty names can be disputed and released to others. So the reservation should be a minimal but honest stub, not an empty shell:

1. Package `agentminutes`, version `0.0.1`.
2. `package.json` description: "Parses agent harness session logs into a unified event schema. Under development."
3. README pointing at agentminutes.dev and the GitHub repo, stating what the library will be and linking the design contract.
4. A trivial real export (for example, a function returning the project URL) so the package is not empty.

Publish:

```bash
npm login
npm publish --access public
```

Later, this name either becomes the thin wrapper that bundles the Go binary, or stays a pointer package.

## 3. PyPI

Same reasoning as npm: PEP 541 lets PyPI reclaim squatted or abandoned names, so publish a minimal honest stub rather than an empty project. Same README and description as the npm stub.

```bash
python -m venv .venv && source .venv/bin/activate
pip install build twine
python -m build
twine upload dist/*
```

Notes:
- PyPI normalizes names, so `agentminutes` also blocks `agent-minutes` and `agent_minutes`. No need to register variants.
- Requires a PyPI account with 2FA and an API token.

## 4. Homebrew

No reservation mechanism exists. When the first real release ships, start with a personal tap (`dacharyc/homebrew-tap`) with an `agentminutes` formula; homebrew-core requires notability (stars, established releases) and can come later. Nothing to do now.

## 5. Skipped registries

- **crates.io:** no Rust component is planned, and crates.io community norms frown on defensive squatting. Skip unless a Rust wrapper becomes real.
- **RubyGems:** no Ruby story. Skip.

## 6. Domain notes

- agentminutes.dev is registered. Remember `.dev` is on the HSTS preload list: the site must serve HTTPS from day one, plain HTTP will not load in browsers.
- agentminutes.com and agentminutes.io were unregistered at decision time. Optional defensive pickups; .com is the only one arguably worth it, and only if the project gains traction.

## Re-verification commands

Run each just before registering (404 or zero means still free):

```bash
curl -s -o /dev/null -w '%{http_code}\n' https://registry.npmjs.org/agentminutes
curl -s -o /dev/null -w '%{http_code}\n' https://pypi.org/pypi/agentminutes/json
curl -s -o /dev/null -w '%{http_code}\n' https://api.github.com/users/agentminutes
gh api 'search/repositories?q=agentminutes+in:name' --jq '.total_count'
```

## Known name-adjacency (no action needed)

- "Agent minutes" is an existing telephony metric (billable minutes per call-center agent); GitHub code search shows ~387 coincidental `agentMinutes`/`agent_minutes` variable hits from that world. Not a product, not a conflict.
- Nearby-but-different products: "Minutes" (useminutes.app, meeting transcription) and Agilisys "Minutes Agent" (council meeting minutes). Different names, different space.
