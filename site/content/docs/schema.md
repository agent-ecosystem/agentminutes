---
title: The Schema
description: The normalized event schema and the guarantees behind it.
icon: schema
weight: 600
---

A normalized session is an ordered list of events. Exactly one payload
field is set per event, matching its `kind`:

| Kind | Payload | ACP analog |
| --- | --- | --- |
| `session_meta` | Session identity: harness, version, session ID, cwd, subagent markers | none (extension) |
| `user_message` | User-role content, with an origin marker: `human` or `harness` | `user_message_chunk` |
| `assistant_message` | One complete assistant API message | `agent_message_chunk` |
| `thinking` | Extended-thinking block | `agent_thought_chunk` |
| `tool_call` | Tool invocation with full input | `tool_call` |
| `tool_result` | Tool outcome, correlated by tool call ID | `tool_call_update` |
| `system` | Harness/API activity: injected context, diagnostics, errors | none (extension) |
| `unknown` | Unclassifiable record, preserved verbatim (permissive mode only) | none (extension) |

## Points worth knowing

- **The event vocabulary is shared.** Event kinds and
  tool classifications follow the
  [Agent Client Protocol](https://agentclientprotocol.com/)'s
  session-update vocabulary where an analog exists. Token usage fields
  follow OTel GenAI semantic conventions. Every field in the schema is
  tagged with its provenance (`acp`, `otel`, or `ext`), enforced by a
  test, so the mapping cannot rot.
- **Token fields carry provider semantics; totals carry a derived
  comparable field.** The field names are shared, and the meanings
  follow the provider: OpenAI-style usage (codex) reports
  `input_tokens` as the total prompt with the cache fields as subsets
  of it (the native `cache_write_input_tokens` maps to
  `cache_creation_input_tokens`), while Anthropic-style usage
  (claude-code) reports it as only the uncached remainder alongside
  disjoint cache read and creation fields. Adapters preserve what the
  harness recorded, so `input_tokens` is never directly comparable
  across harnesses. Session totals therefore also carry
  `total_prompt_tokens`, derived from each harness's documented
  convention: that is the number to compare. See the
  [worked comparison](/docs/example-comparison/#what-the-comparison-shows)
  for a real case where the naive `input_tokens` reading is wrong by
  four orders of magnitude, and
  [Comparing Token Counts](/docs/token-comparison/) for the full field
  guide (caches, reasoning tokens, missing usage, and cost caveats).
- **`assistant_message` is the accounting anchor.** Harnesses may split
  one API message across many records with usage written as a growing
  snapshot. The adapter folds them and takes the final snapshot. Exactly
  one `assistant_message` is emitted per API message, even when all of
  its content became `thinking` or `tool_call` events, so token totals
  are always derivable. Events from the same API message share a
  `message_id`.
- **One session record covers one transcript; a task can span
  several.** A harness that delegates to subagents writes each
  subagent conversation as its own transcript, and each parses to its
  own session with its own `totals`. Where the harness records it
  (claude-code, codex), a subagent's meta shares the parent's
  `session_id` and carries `subagent_id` with `is_subagent: true`, so
  grouping a task is a `session_id` match. Task-scope analysis must
  gather all of a session's transcripts first; see
  [Subagents](/docs/subagents/).
- **`tool_call` and `tool_result` stay separate, in stream order.**
  Ordering is data. Interleaving, parallel tool execution, and retries
  are visible in the sequence. `Session.ToolInteractions()` provides the
  joined view, including unanswered calls and orphaned results.
- **Results carry what the model saw, plus what the harness knew.**
  `tool_result.content` is the post-pipeline content the model actually
  received. Harness sidecar data rides along verbatim in `enrichment`,
  with retrieval metrics promoted to `fetch` (URL, raw bytes fetched,
  status, duration) when present. For summarizing pipelines like Claude
  Code's WebFetch, comparing `fetch.raw_bytes` against the content size
  measures the pipeline's compression directly.
- **Every event points back at its source.** `provenance` carries the
  1-based line range in the native transcript, and optionally the
  verbatim records (`--keep-raw`).
- **The report closes the loop.** `report` counts skipped record types
  (harness UI bookkeeping with no model-visible content), unknown
  events, and orphaned tool results. Between events, skips, and errors,
  every input line is accounted for.

The JSON encoding of `Session` and `Event` is the cross-language output
contract; the `agentminutes_schema` field identifies its revision.
The Go types in
[`session`](https://github.com/agent-ecosystem/agentminutes/tree/main/session)
are documentation for it.
