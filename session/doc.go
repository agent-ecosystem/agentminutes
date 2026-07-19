// Package session defines the unified session schema that agentminutes
// normalizes native harness transcripts into.
//
// The schema's event vocabulary is aligned with the Agent Client Protocol's
// session/update notifications wherever an analog exists, with OpenTelemetry
// GenAI semantic conventions as the naming reference for token usage and
// model attributes. Fields with no counterpart in either are transcript-only
// extensions (timestamps, provenance, threading, harness enrichments).
//
// Every exported struct field carries a `schema` tag classifying its
// provenance, enforced by a test in this package:
//
//   - schema:"acp":  maps to an ACP session/update concept
//   - schema:"otel": named per OTel GenAI semantic conventions
//   - schema:"ext":  transcript-only extension
//
// The JSON encoding of these types is the cross-language output contract of
// the agentminutes CLI. SchemaVersion identifies the contract revision and
// appears on every serialized Session.
package session

// SchemaVersion identifies the revision of the normalized output schema.
// It changes when the JSON encoding of Session or Event changes shape.
const SchemaVersion = "0.1.0"
