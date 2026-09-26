---
title: Go Library
description: Whole-file parsing, streaming, transforms, and the ACP projection.
icon: code
weight: 400
---

## Whole-file parsing

To parse a transcript in one call, use `Parse`:

```go
f, err := os.Open("session.jsonl")
if err != nil {
    return err
}
defer f.Close()

s, err := agentminutes.Parse(f, harness.ClaudeCode, harness.Options{})
if err != nil {
    return err
}

fmt.Println(s.Meta.HarnessVersion, len(s.Events), s.Totals.OutputTokens)
```

`harness.Options` carries the per-parse choices: `Permissive` (preserve
unclassifiable records as `unknown` events instead of failing),
`KeepRaw` (retain native records in provenance), `MaxPayloadBytes`
(replace oversized tool results with a placeholder), `HarnessVersionHint`
(for formats that record no version), `OnSkip` (a callback per skipped
record), and `TextForm` (`TextBare`, the default, or `TextDelivered`,
which puts injected text in a `system` event's `Text` as the harness
delivered it, wrapper included, where the transcript records that form;
the CLI's `--text-form`).

To join tool calls with their results, in call order:

```go
for _, ti := range s.ToolInteractions() {
    if ti.Call == nil {
        continue // orphaned result: no matching call in the transcript
    }
    call := ti.Call.ToolCall
    fmt.Println(call.Name, call.Kind, len(ti.Results))
}
```

Or take the precomputed behavioral summary:

```go
st := s.Stats()
fmt.Println(st.ToolCallsByName, st.ResultBytes, st.WallTimeMS, st.FinalAnswer)
```

## Streaming

For large transcripts or incremental processing, adapters emit events as
an iterator:

```go
a, err := agentminutes.AdapterFor(harness.ClaudeCode)
if err != nil {
    return err
}
for ev, err := range a.Events(f, harness.Options{}) {
    if err != nil {
        return err // a *harness.ParseError identifying harness, version, and line
    }
    if ev.Kind == session.KindToolCall {
        fmt.Println(ev.ToolCall.Name)
    }
}
```

The first event of every stream is `session_meta`, so streaming consumers
know what they are reading before EOF. `session.Accumulator` bridges the
two modes: feed it events, ask it for the accumulated `Session`. This is
exactly what `Parse` does.

## Transforms

Optional post-parse policies compose as `session.Transform` functions,
applied in order by `Parse` or wrapped around `Events` directly. Adapters
translate; transforms reshape, and only when asked. The exported
telemetry promotions (currently `codex.PromotePatchApply` and
`codex.PromoteWebSearch`) are the canonical transforms:

```go
s, err := harness.Parse(codex.Adapter{}, f, harness.Options{}, codex.PromoteWebSearch)
```

## The ACP projection

To project a session onto the
[Agent Client Protocol](https://agentclientprotocol.com/) session/update
vocabulary, use `acp.Project`. It returns a loss report quantifying what
the ACP lens cannot see (system events, token usage, timestamps, empty
anchors):

```go
updates, loss := acp.Project(s)
fmt.Println(len(updates), loss.DroppedEvents, loss.DroppedFields)
```

## Session discovery

`agentminutes.Scan` and `agentminutes.Locate` mirror the CLI's `sessions`
command; see [Session Discovery](/docs/discovery/) for the API and its
accounting discipline.

## Task summaries

`agentminutes.Task` is the library form of `stats --include-subagents`,
producing a task summary:
given a harness and a parent transcript's path, it gathers the
subagent transcripts through the harness's `Locator`, parses each, and
returns a `TaskStats` with the per-transcript summaries, the aggregate,
and the per-agent split.

```go
ts, err := agentminutes.Task(harness.ClaudeCode, "", path, harness.Options{})
if err != nil {
    return err
}
fmt.Println(ts.Join, len(ts.Transcripts), ts.Task.Totals.TotalPromptTokens)
for id, a := range ts.Task.ByAgent {
    fmt.Println(id, a.ToolCalls) // "" is the parent
}
```

The pieces are usable on their own. `harness.Locator.Gather` returns the
`harness.Task` (parent ref, subagent refs, join, and a `Skipped` list
of what it could not include) without parsing, for callers that want
the files; `session.SumStats` aggregates summaries you already hold,
re-deriving `total_prompt_tokens` for the harness's convention so a sum
never mixes conventions (`session.TotalPromptTokens` is that derivation
on its own, for usage you sum yourself); and a session's own
`Stats().ByAgent` carries the inline split for a harness that records
subagents in the parent transcript. `Gather` is part of the
`harness.Locator` interface, so a locator implemented outside this
module must add it; returning the parent alone with
`harness.JoinInline` is the minimal valid implementation. See
[Subagents](/docs/subagents/#task-summaries) for the semantics.
