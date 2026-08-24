package session

import "encoding/json"

// TokenUsage is token accounting for one assistant API message, or a
// session-level sum of per-message accounting. Core fields follow OTel
// GenAI semantic conventions; cache fields are widely reported but not yet
// part of the conventions.
//
// InputTokens and the cache fields carry the provider's own semantics,
// which differ per harness (OTel names the fields but does not resolve
// this): Anthropic-style usage (claude-code) reports the three input
// fields disjoint, summing to the total prompt, while OpenAI-style usage
// (codex) reports InputTokens as the total prompt with the cache fields
// as subsets of it. The same field is therefore not directly comparable
// across harnesses; TotalPromptTokens is the comparable number.
type TokenUsage struct {
	// InputTokens (gen_ai.usage.input_tokens). Provider semantics: the
	// uncached, non-cache-write remainder on claude-code; the total
	// prompt on codex.
	InputTokens int64 `json:"input_tokens" schema:"otel"`

	// OutputTokens (gen_ai.usage.output_tokens).
	OutputTokens int64 `json:"output_tokens" schema:"otel"`

	// CacheReadInputTokens / CacheCreationInputTokens: prompt tokens
	// served from cache and written to cache. Provider semantics: on
	// claude-code both are disjoint from InputTokens; on codex both are
	// subsets of it (cached_input_tokens and cache_write_input_tokens in
	// the native record).
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens,omitempty" schema:"ext"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens,omitempty" schema:"ext"`

	// TotalPromptTokens is the convention-normalized total prompt size,
	// comparable across harnesses by construction. Derived: the
	// accumulator computes it on session totals from the harness's
	// documented usage convention (see totalPromptTokens); it is never
	// reported by a provider, never set on per-message usage, not summed
	// by Add, and zero when the harness's convention is unknown.
	TotalPromptTokens int64 `json:"total_prompt_tokens,omitempty" schema:"ext"`

	// Extra preserves harness-specific usage fields verbatim (e.g. Claude
	// Code's service_tier, inference_geo, speed). Not summed by Add.
	Extra map[string]json.RawMessage `json:"extra,omitempty" schema:"ext"`
}

// Add accumulates v's token counts into u. Extra is not merged (it is
// per-message data with no meaningful sum), and TotalPromptTokens is not
// summed (it is derived from the summed fields, not accumulated).
func (u *TokenUsage) Add(v TokenUsage) {
	u.InputTokens += v.InputTokens
	u.OutputTokens += v.OutputTokens
	u.CacheReadInputTokens += v.CacheReadInputTokens
	u.CacheCreationInputTokens += v.CacheCreationInputTokens
}

// totalPromptTokens returns the total prompt size for usage recorded by
// the named harness, normalizing the provider's documented convention
// (see the TokenUsage field docs and the harness format inventories), or
// zero when the harness's convention is unknown. Keying on the harness
// name is deliberate: the convention is a fact about the provider's
// usage format, recorded here so adapters keep preserving provider
// fields verbatim.
func totalPromptTokens(harness string, u *TokenUsage) int64 {
	switch harness {
	case "claude-code":
		// Anthropic style: the three input fields are disjoint.
		return u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
	case "codex":
		// OpenAI style: input_tokens is already the total prompt.
		return u.InputTokens
	}
	return 0
}
