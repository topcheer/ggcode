package cost

import "testing"

func TestTokenUsage_DisplayInputTokens(t *testing.T) {
	tests := []struct {
		name string
		u    TokenUsage
		want int
	}{
		{"zero value", TokenUsage{}, 0},
		{"no cache read", TokenUsage{InputTokens: 100}, 100},
		{"negative cache read treated as absent", TokenUsage{InputTokens: 100, CacheRead: -5}, 100},
		// Anthropic-style semantics: CacheRead is reported separately from
		// InputTokens and no total is present, so display the raw input count.
		{"no total, cache read separate", TokenUsage{InputTokens: 100, CacheRead: 40}, 100},
		// OpenAI-compat subset semantics (#1529): InputTokens CONTAINS CacheRead.
		{"subset normalization", TokenUsage{InputTokens: 100, CacheRead: 40, PromptTokensTotal: 100}, 60},
		{"subset fully cached", TokenUsage{InputTokens: 100, CacheRead: 100, PromptTokensTotal: 100}, 0},
		// normalized (80) >= InputTokens (50): keep the smaller reported value.
		{"subset normalization floor", TokenUsage{InputTokens: 50, CacheRead: 20, PromptTokensTotal: 100}, 50},
		// normalized would be negative (inconsistent totals): fall back to InputTokens.
		{"subset inconsistent totals", TokenUsage{InputTokens: 100, CacheRead: 150, PromptTokensTotal: 100}, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.u.DisplayInputTokens(); got != tt.want {
				t.Errorf("DisplayInputTokens() = %d, want %d (u=%+v)", got, tt.want, tt.u)
			}
		})
	}
}

func TestFormatCost(t *testing.T) {
	tests := []struct {
		usd  float64
		want string
	}{
		{0, "$0.0000"},
		{0.005, "$0.0050"},
		{0.009999, "$0.0100"}, // rounds up at 4 decimals
		{0.01, "$0.01"},
		{0.1, "$0.10"},
		{1.5, "$1.50"},
		{123.456, "$123.46"},
		{-0.005, "-$0.0050"},
		{-0.02, "-$0.02"},
		{-1234.567, "-$1234.57"},
	}
	for _, tt := range tests {
		if got := FormatCost(tt.usd); got != tt.want {
			t.Errorf("FormatCost(%v) = %q, want %q", tt.usd, got, tt.want)
		}
	}
}
