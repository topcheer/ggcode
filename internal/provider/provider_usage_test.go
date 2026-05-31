package provider

import "testing"

func TestTokenUsageAddReturnsCombinedCopy(t *testing.T) {
	base := TokenUsage{InputTokens: 10, CacheRead: 2}
	sum := base.Add(TokenUsage{OutputTokens: 3, PromptTokensTotal: 13})

	if base.OutputTokens != 0 || base.PromptTokensTotal != 0 {
		t.Fatalf("Add mutated receiver: %+v", base)
	}
	if sum.InputTokens != 10 || sum.OutputTokens != 3 || sum.CacheRead != 2 || sum.PromptTokensTotal != 13 {
		t.Fatalf("Add returned wrong aggregate: %+v", sum)
	}
}

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

func TestTokenUsageDisplayInputTokens(t *testing.T) {
	tests := []struct {
		name  string
		usage TokenUsage
		want  int
	}{
		{
			name:  "openai style prompt tokens subtract cache read",
			usage: TokenUsage{InputTokens: 1200, CacheRead: 800, CacheWrite: 64, PromptTokensTotal: 1200},
			want:  400,
		},
		{
			name:  "anthropic style input stays as non cached input",
			usage: TokenUsage{InputTokens: 23, CacheRead: 8832, CacheWrite: 128, PromptTokensTotal: 8983},
			want:  23,
		},
		{
			name:  "uncached usage stays unchanged",
			usage: TokenUsage{InputTokens: 300, PromptTokensTotal: 300},
			want:  300,
		},
		{
			name:  "invalid prompt total falls back to input tokens",
			usage: TokenUsage{InputTokens: 100, CacheRead: 200, PromptTokensTotal: 50},
			want:  100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.usage.DisplayInputTokens(); got != tt.want {
				t.Fatalf("DisplayInputTokens() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestTokenUsageTotalUsesDisplayedInputTokens(t *testing.T) {
	usage := TokenUsage{InputTokens: 1200, OutputTokens: 300, CacheRead: 800, CacheWrite: 64, PromptTokensTotal: 1200}
	if got := usage.Total(); got != 1500 {
		t.Fatalf("Total() = %d, want 1500", got)
	}
}
