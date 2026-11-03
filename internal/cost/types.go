package cost

import "fmt"

type TokenUsage struct {
	InputTokens       int `json:"input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	CacheRead         int `json:"cache_read_tokens"`
	CacheWrite        int `json:"cache_write_tokens"`
	PromptTokensTotal int `json:"prompt_tokens_total,omitempty"`
}

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
