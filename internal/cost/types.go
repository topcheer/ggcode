package cost

import "fmt"

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

// FormatCost renders a USD amount (moved here from the retired manager -
// it formats LIVE per-session costs, not the dead .cost.json store).
func FormatCost(usd float64) string {
	sign := ""
	if usd < 0 {
		sign = "-"
		usd = -usd
	}
	if usd < 0.01 {
		return fmt.Sprintf("%s$%.4f", sign, usd)
	}
	return fmt.Sprintf("%s$%.2f", sign, usd)
}
