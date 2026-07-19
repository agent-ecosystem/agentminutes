package session

import "encoding/json"

// TokenUsage is token accounting for one assistant API message, or a
// session-level sum of per-message accounting. Core fields follow OTel
// GenAI semantic conventions; cache fields are widely reported but not yet
// part of the conventions.
type TokenUsage struct {
	// InputTokens (gen_ai.usage.input_tokens).
	InputTokens int64 `json:"input_tokens" schema:"otel"`

	// OutputTokens (gen_ai.usage.output_tokens).
	OutputTokens int64 `json:"output_tokens" schema:"otel"`

	CacheReadInputTokens     int64 `json:"cache_read_input_tokens,omitempty" schema:"ext"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens,omitempty" schema:"ext"`

	// Extra preserves harness-specific usage fields verbatim (e.g. Claude
	// Code's service_tier, inference_geo, speed). Not summed by Add.
	Extra map[string]json.RawMessage `json:"extra,omitempty" schema:"ext"`
}

// Add accumulates v's token counts into u. Extra is not merged: it is
// per-message data with no meaningful sum.
func (u *TokenUsage) Add(v TokenUsage) {
	u.InputTokens += v.InputTokens
	u.OutputTokens += v.OutputTokens
	u.CacheReadInputTokens += v.CacheReadInputTokens
	u.CacheCreationInputTokens += v.CacheCreationInputTokens
}
