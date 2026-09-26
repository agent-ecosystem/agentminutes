# Model-visible text: design

Status: designed and built (September 2026). Grew out of the second
consumer's finding (skillxp, issue #3) that the Copilot adapter kept a
delivered skill body in a `system` event's `Details` with `Text` empty,
so a phrase traced from a SKILL.md never surfaced as harness-injected
content. The audit that followed found the same gap on Claude Code
(reminders whose text had moved to a new key, the system prompt,
CLAUDE.md bodies, six attachment types whose only text is the rendered
form) and Codex (the system prompt). This records the decisions.

## What `Text` promises

A `system` event's `Text` is the record's model-visible text when it
carries one: a system prompt, an injected reminder or listing, a
delivered skill body, a queued prompt. Diagnostics (an abort reason, an
API error, a session notice) put their message there too, which the
model never saw; `Subtype` tells the two apart, and no visibility field
was added, because adapters often cannot know the role the model
received a text in (Copilot records that it delivered a skill body and
hashes it, but not in which role). A tool result's model-visible text is
its content; the sidecar is `Enrichment`.

Filling `Text` is translation, not policy: it asserts nothing about
role. Where the transcript records the role (Claude Code's `isMeta`
user records), the record is a `user_message` with origin `harness`;
where it does not (Copilot's `skill.invoked`), the event stays `system`
and a conversation-shaped synthesis would be a transform's job. Nobody
has asked for one.

## Bare and delivered

Two forms of injected text exist when the harness records both the text
it injected and the framing it wrapped it in: Claude Code's rendered
`<system-reminder>` blocks (2.1.267+, one per attachment; the
attachment-to-message renderer in the bundle emits each as an `isMeta`
user message) and Copilot's `<skill-context>` wrapper (named by the
delivery record with the body's SHA-256).

- **Bare** (default): the injected text without the tag wrapper. For
  Claude Code this is the rendered block with the `<system-reminder>`
  tags stripped whenever the record has one; the attachment's own
  fields (`content`, `text`, or a type-specific field) are the fallback
  for older records. Rendered wins over the fields because the fields
  do not always reconstruct the message (`deferred_tools_delta` renders
  two lists from two fields; `instructions` adds a per-file header) and
  because it is drift-proof for attachment types not yet modeled. For
  Copilot this is the skill body inside the wrapper's content lines
  with only the tag lines stripped: after `<skill-context name="...">`
  the prefix states the skill's base directory and, when the skill
  directory holds other files, lists every one of them (recursively,
  unfiltered, files the body never mentions included), which is content
  the harness composed for the model and the only record of a platform
  behavior (resource enumeration) no other harness has. Stripping the
  whole prefix, as the first version did, left a consumer on the
  default form blind to it (issue #4).
- **Delivered** (`harness.Options.TextForm = TextDelivered`, CLI
  `--text-form delivered`): the rendered block verbatim, or the skill
  body inside its recorded prefix and suffix. For experiments that must
  reproduce what the model saw byte for byte. Records with no recorded
  delivered form keep bare text.

This is an `Options` field rather than a `session.Transform`, unlike
telemetry promotion (`plans/telemetry-promotion.md`): both forms are
translations of the same record, nothing is synthesized or marked, and
the Copilot forms need parser state (a one-record hold until the
delivery record arrives, in both forms now that the bare form takes
the wrapper's content lines), which a transform over the emitted stream
could only recover by buffering.

## Repeat activations logged by reference

Copilot logs a repeat activation of a skill whose body is unchanged as
`skill.invoked_ref` (the `skill.invoked` fields with `contentId` and
`contentLength` in place of `content`), followed by a fresh
`skill.context_delivered_ref` hashing the same body. The harness
delivered the body again; the transcript stores it by reference (an
edited body between activations logs a full `skill.invoked`, so this
is log deduplication by content hash, not delivery deduplication). The
adapter keeps every `skill.invoked` body by its hash and resolves the
ref to it, so the second activation is a `system` event with the same
text as the first, in either form, and a consumer tracing a body phrase
sees both deliveries. A ref no earlier record in the transcript resolves
(a truncated copy) is still emitted, textless, with the ref fields in
`Details`, so the delivery still counts. Verified against the
agent-skill-implementation benchmark's reactivation transcript
(1.0.88, 2026-09-25), which the copilot baseline now includes.

Verified in the 2.1.274 bundle rather than inferred: the system prompt
sections are joined by a blank line with empty sections and the
`__SYSTEM_PROMPT_DYNAMIC_BOUNDARY__` sentinel skipped (the harness also
hoists its identity block and splits at the sentinel into separately
cached blocks, so the joined text is the prompt's text, not its block
layout); every attachment renderer emits at most one text message, so a
record with several rendered entries cannot occur yet (the join with a
blank line is defined but unexercised); and a task notification's two
renderings (`rendered`, `renderedInHumanTurn`) are chosen by position
with a structural predicate (`oNe`/`eXo`: the human-turn form when the
nearest preceding conversational record is a human prompt) that the
adapter tracks in the same way. One predicate on user records in that
walk could not be resolved from the minified bundle; every corpus
instance is mid-turn.

## Queued prompts stay system events

A prompt the user typed while the model worked (`queued_command`,
`commandMode: prompt`, `origin.kind: human`) is delivered as an
attachment and never re-recorded as a `user` record (30 of 31 in the
corpus). It is a `system` event with the prompt as `Text`: record type
decides kind, the harness itself chose to deliver it as a reminder, and
user-message counts follow the harness's turn structure. The origin
marker exists for the opposite case. Authorship is in `Details`.

## The invariants

`internal/textaudit`, over every fixture in both forms and the local
corpus, for `system` events (Text against Details) and tool results
(content against Enrichment):

- **Untexted**: an event that surfaces no text carries no string of 80+
  bytes in its details.
- **Uncontained**: an event that surfaces text carries no long string
  that neither contains nor is contained by that text (a second text
  the event does not surface).

Every violation is either a fix or an entry in the adapter's `nonText`
map with a reason: a path, bytes, tool schemas, the call's own input, a
UI rendering the model never received, an echo of text surfaced by
another event, promotable telemetry. Never a reason-less entry; when the
reason is "the transcript does not say", write that and record the open
question in the inventory. The tool-result half is where the "which
field did the model see" choice is pinned: the raw file behind a
numbered rendering, the persisted-output case (the model sees a notice
and a preview; the sidecar keeps 30 KB of stdout), an error with an
`Error:` prefix that content wraps in tags.

Two per-harness **delivery-marker** checks have no length threshold,
which the length rule needs: a Claude Code attachment with a rendered
form must surface text; a Copilot delivery record's hash must match
the body inside the activation event just before it (`skill.invoked`
or `skill.invoked_ref`), in the wrapper of the form being parsed. The
check is per delivery, so a repeat delivery cannot pass on the
strength of the first activation's text, which is how the unresolved
`skill.invoked_ref` went unnoticed.

Known limits, deliberately not closed: short injected text on a record
with no delivery marker (the length rule cannot see it); the threshold
counts bytes, not characters; `unknown` events in permissive mode are
not audited; the audit runs with default options, so truncated results
are not covered.

## Schema impact

`0.1.0` to `0.2.0`: a Claude Code attachment's `details` is the whole
record (as for `system` records) rather than the attachment object, so
attachment fields moved under `attachment.<key>` and the envelope's
`rendered` keys are preserved; every Codex session gains a
`session_meta/base_instructions` system event on the meta line. No
field was added or removed.
