---
title: Design Notes
description: Loud failure, post-hoc parsing, streaming, and the explicit registry.
icon: architecture
weight: 800
---

The behaviors agentminutes encodes, made explicit:

- **Loud failure.** An unrecognized record type or a malformed record is
  a parse error identifying the harness, harness version, and line,
  never a silent skip. Dropped events corrupt behavioral metrics; if you
  prefer degraded output over failure, opt in with `--permissive` and
  the dropped-nothing guarantee moves into `unknown` events. Adapters
  are tested with a line-accounting invariant that mechanically verifies
  the guarantee.
- **Model-visible text is surfaced, never buried.** A `system` event's
  `text` is the record's model-visible text when it carries one (a
  system prompt, an injected reminder, a delivered skill body), and a
  tool result's `content` is what the model received, with the
  harness's sidecar in `enrichment`. Consumers that trace a phrase
  across a session read those fields and never harness-shaped
  `details`, so an adapter that left text there would hide it. A second
  invariant enforces this: every long string an event keeps in its
  details or enrichment either is accounted for by the event's text or
  is enumerated per adapter with the reason it is not text (a path,
  bytes, a tool schema, a UI rendering the model never saw), and where
  a harness records that it delivered text, the adapter is held to that
  record with no length threshold.
- **Parsing is post-hoc, never in-band.** agentminutes reads the
  transcripts harnesses already write. Driving harnesses through adapter
  protocols could perturb the behavior being measured, so ACP
  contributes its schema here, not its transport.
- **Streaming is the core.** Adapters emit
  `iter.Seq2[session.Event, error]`; whole-file parsing is a thin
  accumulator over it. Note that streaming does not guarantee constant
  memory for every harness: line-oriented formats (Claude Code) stream
  genuinely, while single-document formats must buffer internally.
- **Explicit registry.** Supported adapters are a visible list in the
  facade, never `init()` side effects.

## Contributing an adapter

Adapters live under
[`harness/`](https://github.com/agent-ecosystem/agentminutes/tree/main/harness),
one package per harness, implementing the `harness.Adapter` interface.
To add one, follow
[DEVELOPMENT.md](https://github.com/agent-ecosystem/agentminutes/blob/main/DEVELOPMENT.md):
it covers the full process from generating fresh ground-truth
transcripts and writing a format inventory through implementation
conventions, the standard test suite, registration, and verification.
