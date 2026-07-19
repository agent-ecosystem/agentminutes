# Telemetry promotion: design

Status: designed and built. Grew out of the drift-probe finding
that Codex 0.144.1 records URL fetches only as `event_msg` telemetry
(`web_search_end`), leaving retrieval invisible to tool metrics (see the
codex format inventory).

## The problem

Some harness telemetry is the *only* record of a model action. The mapping
rule says telemetry becomes `system` events (preserved verbatim, never
conversation events), which is correct as a default: promoting telemetry
to tool events risks double-counting on harness versions that record the
same action both ways (Codex 0.118 wrote a `response_item`
`web_search_call`; 0.144 writes only the telemetry). But a consumer whose
experiment needs fetch counts on 0.144 transcripts is stuck: without
promotion, `Stats()` and `ToolInteractions()` never see those fetches.

## Decision: opt-in stream transforms, not adapter options

The user decides, at runtime, per parse; the library ships the mapping
knowledge but never applies it by default. Constructs considered:

1. **Namespaced strings in `harness.Options`** (`Promote: []string{"codex:web_search_end"}`)
   — rejected: a string protocol threaded through a typed API; typos need
   runtime validation; the shared leaf package stays clean only by making
   the values opaque.
2. **Typed fields on the adapter struct** (`codex.Adapter{PromoteWebSearch: true}`)
   — rejected on semantics, though it is defensible Go (`http.Transport`
   precedent): the `Adapter`'s contract is translation with total
   accounting, and promotion is representational policy over
   already-translated events, not translation.
3. **Stream transforms** (chosen): promotion is a pure function over the
   unified event stream. The `system` event already carries everything the
   synthesis needs (payload in `Details`, provenance, timestamp, subtype).

```go
// session package, generic:
type Transform func(iter.Seq2[Event, error]) iter.Seq2[Event, error]

// adapter package, one exported rule per promotion:
func codex.PromoteWebSearch(events ...) ...

// call sites compose policy visibly:
harness.Parse(adapter, r, opts, codex.PromoteWebSearch)
```

`harness.Parse` (and the facade `agentminutes.Parse`) take variadic
transforms, applied in order over `Adapter.Events`. Streaming consumers
wrap the sequence themselves.

## Rules for promotion transforms

- **Replace, don't add**: the transform consumes the `system` event and
  yields the synthesized pair; one native record never has three
  representations in a single parse.
- **Same provenance**: synthesized events carry the source event's
  provenance, so line accounting holds by construction.
- **Marked**: synthesized events set `promoted_from` (extension field on
  `ToolCall` and `ToolResult`, e.g. `"event_msg/web_search_end"`). This is
  the only runtime record that promotion happened; it is what lets a
  consumer audit or dedupe against natively-recorded duplicates. A
  transform that does not mark its output is wrong.
- **Mirror native synthesis**: the shape follows the adapter's own
  synthesis for the equivalent native record (for web search: the 0.118
  `response_item` handling — name `web_search`, kind `fetch`, action as
  input, payload in enrichment, correlation from `call_id` falling back to
  a line-derived ID).
- **No-op elsewhere**: transforms match on their own harness's subtypes,
  so applying one to another harness's stream does nothing (still, the CLI
  only attaches a transform when the resolved harness matches).
- **Off by default, everywhere**: the facade registry, drift probe/scan,
  and every existing path parse without transforms. The drift probe stays
  promotion-free on principle: it measures what the harness wrote, not
  what we can synthesize from it.

## CLI boundary

`--promote codex:web-search` (repeatable) on `convert` and `stats`. Flags
are strings, so this is where the one string table lives; unknown names
and harness mismatches error loudly.

## Schema impact

`ToolCall.PromotedFrom` / `ToolResult.PromotedFrom`, `schema:"ext"`,
`promoted_from,omitempty`. SchemaVersion stays 0.1.0: additive optional
extension fields on a schema that has never been released.
