# Antigravity CLI transcript format: empirical inventory

Status: findings (updated with paid-tier probe results)
Source: thirteen local conversations generated with Antigravity CLI (`agy`) 1.1.1: eight from the initial (partly malformed-flag) evaluation, plus five clean probes on a Google AI Pro subscription covering plain Q&A, shell (RUN_COMMAND), file write (CODE_ACTION), URL fetch (READ_URL_CONTENT with side file), and an attempted parallel-tool case. Plus web research on the Gemini CLI transition. Re-validated on 1.1.2 and 1.1.3 by clean drift probes (qa, shell, file, fetch, multi probes exercised and parsed; vocabulary unchanged against the baseline). Re-validated on 1.1.8 by drift probe (all five probes exercised and parsed): one additive key, `exit_code` (int) on `RUN_COMMAND` result records, and the RUN_COMMAND content template now reads "The command exited with code N." instead of "The command completed successfully." (content is kept verbatim either way; the adapter leaves `exit_code` unread — `error` remains the error signal). 1.1.8 also added headless JSON output (see Headless usage). Note: agy auto-updates itself in place (1.1.2 → 1.1.3 observed within an hour, no user action), so the probe version gate can trigger without a deliberate upgrade.

Clean fixture conversations (brain dir IDs) for the future adapter: `9e2ef85c` (Q&A), `70241608` (shell), `5f6c0eb7` (file write), `22622b27` (fetch), `5e2b84cc` (two sequential tools).

## Context: why Antigravity replaced Gemini CLI as the third harness

Classic Gemini CLI stopped serving requests for individuals (free, Google AI Pro/Ultra, individual Code Assist) on June 18, 2026; only enterprise Code Assist licenses retain access. Attempting to use it yields `IneligibleTierError`. The Homebrew `gemini-cli` formula is deprecated (disabled 2026-12-18) with `antigravity-cli` as the stated replacement. Antigravity CLI is Google's successor harness: Go binary (`agy`), shares the agent harness with the Antigravity 2.0 desktop app.

Verdict from this evaluation: **viable as the third harness, with caveats** (no token usage in transcripts, positional tool correlation, templated tool outputs, tight free-tier quota). Details below.

## Headless usage

```bash
agy --dangerously-skip-permissions --add-dir "$WORKDIR" -p "prompt"
```

- **`--print`/`-p` takes the prompt as its flag value.** Flags placed after `--print` are consumed as the prompt: `agy --print --dangerously-skip-permissions "real prompt"` sends the literal string `--dangerously-skip-permissions` to the model, which then improvises (observed: minutes of unsupervised exploration, running git commands and grepping unrelated workspaces). Put `-p "prompt"` last.
- `--dangerously-skip-permissions` auto-approves tools (`--yes` does not exist).
- `--add-dir` sets the workspace; without it, `agy` operates in its own `~/.gemini/antigravity-cli/scratch` and ignores the invoking cwd.
- Auth is a one-time interactive Google OAuth (needs a real TTY; fails under Claude Code's `!` shell). Stored globally.
- On 1.1.1, `--print` output survives piping to a non-TTY (earlier versions had a known stdout-drop bug, fixed).
- `--model` selects the model; observed default "Gemini 3.5 Flash (Medium)".
- From 1.1.8, `--output-format json` works in print mode and the result envelope carries `conversation_id` in-band (agentsummons 0.3.0 assembles it via `Request.JSONOutput`). Consequence for this library's discovery seam: a headless runner using JSON output gets the conversation ID directly and can `Locate` the transcript by ID; post-hoc time-window discovery (`agentminutes sessions`) is only needed in text mode or on agy older than 1.1.8. Resume (`--conversation <id>`) appends to the same conversation directory rather than forking a new one.

**Quota warning:** the free individual tier was exhausted by a few minutes of runaway agentic exploration (~250 steps total), with a ~7-day reset. The experiment's Antigravity leg (~720 invocations) cannot run on the free tier.

### Paid usage options

- **Subscriptions** (account-based, the primary path): Google AI Pro $20/mo (entry quota, refreshes every 5 hours), AI Ultra $100/mo (~5x Pro), AI Ultra Max $200/mo (~20x Pro). Exact quota numbers are unpublished and vary by model and load; community reporting says Pro "handles sequential agent requests without issues," which matches the experiment's one-question-at-a-time pattern. AI credits exist as an overage mechanism on top of plans.
- **Pay-as-you-go**: an AI Studio Gemini API key is NOT supported (confirmed by maintainers, June 2026, no timeline). The supported PAYG path is attaching a **GCP project ID** to Antigravity 2.0 or the CLI (enterprise documentation); billing is per-token. For scale: Gemini 3.5 Flash API rates are $1.50/M input, $9.00/M output (July 2026). A short agentic doc-QA invocation (~5 steps, ~75K cumulative input, ~1.5K output) costs roughly $0.12, putting the full ~720-invocation leg in the $50-150 range at API rates. PAYG avoids quota-window pacing entirely (no throttling confound mid-run) but needs GCP project setup, and whether Antigravity PAYG bills at exactly these API rates is unverified.
- **Recommended sequence**: subscribe to Pro ($20) for the experiment month and measure; upgrade to Ultra ($100) or switch to GCP PAYG only if Pro's 5-hour quota windows can't sustain the run cadence. Record which tier/path was used in run metadata, since throttling behavior is an experimental confound.

## Storage layout (`~/.gemini/antigravity-cli/`)

| Path | Content | Post-hoc readable? |
| --- | --- | --- |
| `brain/<conversation-uuid>/.system_generated/logs/transcript_full.jsonl` | Full step-record transcript (see schema below) | **Yes: plaintext JSONL** |
| `brain/<conversation-uuid>/.system_generated/logs/transcript.jsonl` | Same record count as `_full` in 1.1.1 (delta unverified) | Yes |
| `brain/<conversation-uuid>/.system_generated/steps/<n>/content.md` | Side files: e.g. the **raw fetched page** for READ_URL_CONTENT steps (observed 22.9KB raw HTML with title/source header) | Yes |
| `conversations/<uuid>.db` | SQLite, protobuf `step_payload` blobs, schema unpublished | Best-effort only |
| `implicit/<uuid>.pb` | AES-GCM encrypted (cloud-sync copies); daemon JSON-RPC (`GetCascadeTrajectory`) can decrypt while running | Not needed |
| `history.jsonl`, `conversation_summaries.db`, `cli.log` | Prompt recall, summaries, CLI logs | Yes |

The key finding vs. earlier third-party reports: **the brain JSONL transcripts are complete enough for our purposes and require no daemon.** Tool calls appear with full args; tool outputs appear with full content.

## transcript_full.jsonl record schema

One JSON object per line. Keys observed across all conversations: `step_index`, `source` (`USER_EXPLICIT` | `MODEL` | `SYSTEM`), `type`, `status` (`DONE`), `created_at` (RFC3339 UTC, seconds), `content`, `tool_calls`, `thinking`, `error`, `error_code`, and from 1.1.8 `exit_code` (int, on `RUN_COMMAND` results).

Record types observed:

| type | source | Content |
| --- | --- | --- |
| `USER_INPUT` | USER_EXPLICIT | Prompt wrapped in `<USER_REQUEST>` tags plus `<ADDITIONAL_METADATA>` (local time) and `<USER_SETTINGS_CHANGE>` (model selection, the only place the model name appears) |
| `CONVERSATION_HISTORY` | SYSTEM | Marker, no content |
| `PLANNER_RESPONSE` | MODEL | Assistant turn: optional `content` (text), optional `thinking`, optional `tool_calls: [{name, args}]` with **full args** (paths, commands, plus UI strings `toolAction`/`toolSummary`) |
| `RUN_COMMAND`, `VIEW_FILE`, `LIST_DIRECTORY`, `GREP_SEARCH`, `SEARCH_WEB`, `READ_URL_CONTENT`, `CODE_ACTION`, `GENERIC` | MODEL | Tool results: full output as templated text ("The command completed successfully. Output: ...", files get line numbers prepended, timestamps embedded). READ_URL_CONTENT saves raw content to a `steps/<n>/content.md` side file and records the path. CODE_ACTION is the result of `write_to_file` (the tool call args carry the complete file content in `CodeContent`) |
| `CHECKPOINT` | SYSTEM | Synthetic context summary; appears even in short fresh conversations claiming truncation |
| `ERROR_MESSAGE` | SYSTEM | `error` text + `error_code` (e.g. failed tool-call parses) |
| `ASK_QUESTION` | MODEL | User-facing question; records "User Skipped" in headless mode |
| `SYSTEM_MESSAGE` | SYSTEM | Harness-injected notice wrapped in `<SYSTEM_MESSAGE>` tags in `content` (first observed in a live transcript after the 1.1.3 validation probes: a server-restart notice about stopped subagents/background tasks). No `error`/`error_code` |

## Mapping sketch (for a future `harness/antigravity` adapter)

- `USER_INPUT` → `user_message` (strip wrapper tags; `<USER_REQUEST>` body is the prompt; metadata/settings-change parts are harness-injected)
- `PLANNER_RESPONSE` → `assistant_message` (+ `thinking` event when the key is present) + one `tool_call` event per `tool_calls[]` entry
- Tool step records → `tool_result`
- `CHECKPOINT`, `CONVERSATION_HISTORY`, `SYSTEM_MESSAGE`, `GENERIC` (permission listings), `ASK_QUESTION` → `system`
- `ERROR_MESSAGE` → `system` with level error

Known problems the adapter must solve:

1. **JSONL line order does not match step order.** Observed: a CHECKPOINT with `step_index` 4 appended as the last line of the file, after steps 5-7. The adapter must sort by `step_index` (recording both file line and step index in provenance) rather than assuming stream order.
2. **No correlation IDs.** `tool_calls` entries have no call IDs; correlation is positional and empirically consistent: a PLANNER_RESPONSE with one call at step N is followed by its result record at step N+1 (verified across all clean probes, including with CHECKPOINT records interleaved elsewhere). Synthesize IDs (`<conv>:step<N>:call<i>`). Multiple calls in one PLANNER_RESPONSE were never observed (the "do two things" probe ran them as two sequential single-call rounds); handle arrays defensively and flag multi-call pairing as unverified.
3. **No token usage anywhere** in the transcripts, on free or paid tier (verified across all record keys). If the experiment needs usage for this harness, it must come from OTel export (unverified whether Antigravity supports it) or be marked unavailable.
4. **Use `transcript_full.jsonl`, not `transcript.jsonl`.** Same record counts, different content: on the fetch-heavy probe, full is ~8.4KB larger (fetched content retained vs. trimmed). The short variant is not a safe substitute.
5. **Templated tool outputs.** VIEW_FILE injects line numbers; RUN_COMMAND wraps output in boilerplate; CODE_ACTION results embed instruction text aimed at the model. Keep content verbatim (never de-template lossily) and note the convention.
6. **Model identity** is only derivable from the `<USER_SETTINGS_CHANGE>` text in USER_INPUT ("Gemini 3.5 Flash (Medium)" on both free and Pro tiers), a human-readable label rather than a model ID.
7. **The conversation ID is the brain directory name**; records carry no session/conversation ID.

Tool names observed for kind mapping: `run_command` family (RUN_COMMAND results), `write_to_file` (CODE_ACTION), `view_file`, `list_dir`, `grep_search`, `search_web`, `read_url_content`, `list_permissions`.

## RQ3 payoff

READ_URL_CONTENT persists the raw fetched page (side file) *and* the summary/extract the model received (step content). Like Claude Code's WebFetch bytes-vs-content, this gives both sides of the fetch-pipeline compression measurement. SEARCH_WEB records the query and a result summary.

## Open items before building the adapter

- ~~Generate clean fixtures~~ Done on Pro tier (conversation IDs listed at top); copy sanitized versions into the adapter's testdata when building.
- ~~Verify the transcript vs transcript_full delta~~ Done: use `transcript_full.jsonl` (see problem 4 above).
- Check whether Antigravity exports OTel (for token usage).
- Multi-call PLANNER_RESPONSE pairing remains unobserved; the model decomposes parallel requests into sequential single-call rounds. Handle defensively.
- Track format drift: Antigravity is churning fast (headless bugs fixed within point releases); pin `agy` version in all fixtures and run metadata.
- Pro-tier quota comfortably handled 5 sequential agentic probes in minutes with no throttling; sustained Phase 2 cadence still to be measured.
