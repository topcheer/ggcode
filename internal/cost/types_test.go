package cost

import "testing"

func TestFormatCost(t *testing.T) {
	tests := []struct {
		name string
		usd  float64
		want string
	}{
		{"zero", 0, "$0.0000"},
		{"tiny", 0.00004, "$0.0000"},
		{"sub-cent rounds to 4 decimals", 0.005, "$0.0050"},
		{"sub-cent", 0.009, "$0.0090"},
		{"sub-cent edge", 0.0099, "$0.0099"},
		{"boundary: exactly one cent switches to 2 decimals", 0.01, "$0.01"},
		{"above boundary uses 2 decimals", 0.125, "$0.12"},
		{"one dollar", 1, "$1.00"},
		{"one and a half", 1.5, "$1.50"},
		{"rounding up at 2 decimals", 123.456, "$123.46"},
		{"carry into next dollar", 99.999, "$100.00"},
		{"negative sub-cent", -0.005, "-$0.0050"},
		{"negative boundary", -0.01, "-$0.01"},
		{"negative above boundary", -2.5, "-$2.50"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatCost(tt.usd); got != tt.want {
				t.Errorf("FormatCost(%v) = %q, want %q", tt.usd, got, tt.want)
			}
		})
	}
}

// TestTokenUsage_DisplayInputTokens pins the #1529 provider-semantics
// normalization: for OpenAI-compat endpoints (PromptTokensTotal > 0),
// InputTokens contains CacheRead and the display value is the uncached
// remainder; for Anthropic-style usage (no prompt total) InputTokens
// already excludes cached reads and is returned as-is.
func TestTokenUsage_DisplayInputTokens(t *testing.T) {
	tests := []struct {
		name string
		u    TokenUsage
		want int
	}{
		{"no cache fields", TokenUsage{InputTokens: 100}, 100},
		{"zero cache read", TokenUsage{InputTokens: 100, CacheRead: 0}, 100},
		{"negative cache read", TokenUsage{InputTokens: 100, CacheRead: -1}, 100},
		{"anthropic semantics: no prompt total", TokenUsage{InputTokens: 100, CacheRead: 20}, 100},
		{"openai subset: normalized to remainder", TokenUsage{InputTokens: 50, CacheRead: 20, PromptTokensTotal: 60}, 40},
		{"openai subset: remainder >= input keeps input", TokenUsage{InputTokens: 80, CacheRead: 20, PromptTokensTotal: 120}, 80},
		{"openai subset: remainder == input keeps input", TokenUsage{InputTokens: 100, CacheRead: 20, PromptTokensTotal: 120}, 100},
		{"openai subset: cache exceeds total keeps input", TokenUsage{InputTokens: 50, CacheRead: 70, PromptTokensTotal: 60}, 50},
		{"all zero", TokenUsage{}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.u.DisplayInputTokens(); got != tt.want {
				t.Errorf("DisplayInputTokens(%+v) = %d, want %d", tt.u, got, tt.want)
			}
		})
	}
}
