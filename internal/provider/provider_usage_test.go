package provider

import "testing"

func TestTokenUsageCacheHitPercent(t *testing.T) {
	tests := []struct {
		name  string
		usage TokenUsage
		want  int
	}{
		{
			name:  "zero without prompt tokens",
			usage: TokenUsage{},
			want:  0,
		},
		{
			name:  "zero without cache read",
			usage: TokenUsage{InputTokens: 1200, PromptTokensTotal: 1200},
			want:  0,
		},
		{
			name:  "uses normalized prompt total when available",
			usage: TokenUsage{InputTokens: 400, CacheRead: 800, PromptTokensTotal: 1200},
			want:  67,
		},
		{
			name:  "falls back for anthropic-style split counters",
			usage: TokenUsage{InputTokens: 23, CacheRead: 8832, CacheWrite: 128},
			want:  98,
		},
		{
			name:  "falls back for mixed legacy aggregate with partial prompt total",
			usage: TokenUsage{InputTokens: 7749172, CacheRead: 5583616, PromptTokensTotal: 1193080},
			want:  72,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.usage.CacheHitPercent(); got != tt.want {
				t.Fatalf("CacheHitPercent() = %d, want %d", got, tt.want)
			}
		})
	}
}
