package session

import "iter"

// Transform is a post-parse policy over the unified event stream: it takes
// the sequence an adapter produced and yields a reshaped one. Adapters
// translate with total accounting; transforms make representational
// choices the caller opts into (the telemetry promotions in the adapter
// packages, e.g. codex.PromoteWebSearch, are the canonical examples).
//
// Rules for well-behaved transforms: replace events rather than adding
// parallel representations, preserve the source event's provenance so line
// accounting holds, mark synthesized tool events via PromotedFrom, and
// pass errors through untouched. See plans/telemetry-promotion.md.
type Transform func(iter.Seq2[Event, error]) iter.Seq2[Event, error]
