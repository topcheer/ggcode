package cost

// TokenUsage records token consumption for a single API call.
// Defined in the cost package to avoid circular imports.
type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	CacheRead    int `json:"cache_read_tokens"`
	CacheWrite   int `json:"cache_write_tokens"`
	// PromptTokensTotal carries the OpenAI-compat subset semantics (#1529):
	// when >0, InputTokens CONTAINS CacheRead (PromptTokensTotal is the
	// whole and CacheRead the subset) and DisplayInputTokens() normalizes
	// to the uncached remainder.
	PromptTokensTotal int `json:"prompt_tokens_total,omitempty"`
}

// DisplayInputTokens returns the input tokens EXCLUDING cached reads for
// every provider semantics (#1529, mirrors provider.TokenUsage's method
// so the cost layer and the UI agree on one number).
func (u TokenUsage) DisplayInputTokens() int {
	if u.CacheRead <= 0 {
		return u.InputTokens
	}
	if u.PromptTokensTotal > 0 {
		if normalized := u.PromptTokensTotal - u.CacheRead; normalized >= 0 {
			if normalized < u.InputTokens {
				return normalized
			}
			return u.InputTokens
		}
	}
	return u.InputTokens
}
