package provider

import (
	"testing"
)

func TestLookupKnownModelContextWindow(t *testing.T) {
	tests := []struct {
		model string
		want  int
	}{
		// Claude models
		{"claude-sonnet-4-20250514", 200_000},
		{"claude-opus-4-20250514", 200_000},
		{"claude-3-5-sonnet-20241022", 200_000},
		{"claude-3-7-sonnet-20250219", 200_000},
		{"Claude-3-Haiku-20240307", 200_000}, // case insensitive

		// GPT models
		{"gpt-4o-2024-08-06", 128_000},
		{"gpt-4o-mini", 128_000},
		{"gpt-4.1", 1_000_000},
		{"gpt-4.1-mini", 1_000_000},
		{"gpt-4.1-nano", 1_000_000},
		{"o3-mini", 200_000},
		{"o1", 200_000},

		// Gemini models
		{"gemini-2.5-pro", 1_000_000},
		{"gemini-2.0-flash-001", 1_000_000},
		{"gemini-1.5-pro-latest", 2_000_000},

		// DeepSeek
		{"deepseek-r1", 64_000},
		{"deepseek-chat", 64_000},
		{"deepseek-coder-v2", 64_000},

		// Qwen
		{"qwen-plus", 131_072},
		{"qwen2.5-coder-32b", 131_072},
		{"qwen3-235b-a22b", 131_072},

		// GLM
		{"glm-4-plus", 128_000},
		{"glm-4-flash", 128_000},
		{"glm-4-long", 1_000_000},

		// Mistral
		{"mistral-large-latest", 128_000},
		{"codestral-latest", 256_000},

		// Llama
		{"llama-3.1-70b-instruct", 128_000},
		{"llama-3.3-70b-versatile", 128_000},

		// Unknown models
		{"my-custom-model", 0},
		{"", 0},
		{"unknown-future-model", 0},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := LookupKnownModelContextWindow(tt.model)
			if got != tt.want {
				t.Errorf("LookupKnownModelContextWindow(%q) = %d, want %d", tt.model, got, tt.want)
			}
		})
	}
}

func TestKnownModelPrefixOrdering(t *testing.T) {
	// gpt-4 should not shadow gpt-4.1 because gpt-4.1 is listed before gpt-4.
	// gpt-4 has a much smaller window (8192) than gpt-4.1 (1M).
	if got := LookupKnownModelContextWindow("gpt-4.1"); got != 1_000_000 {
		t.Errorf("gpt-4.1 should get 1M, got %d — check table ordering", got)
	}
	if got := LookupKnownModelContextWindow("gpt-4-turbo"); got != 128_000 {
		t.Errorf("gpt-4-turbo should get 128K, got %d", got)
	}
}

func TestKnownModelCaseInsensitive(t *testing.T) {
	// All these should match regardless of case.
	for _, m := range []string{"GPT-4o", "Gpt-4O", "Claude-Sonnet-4", "CLAUDE-3-5-SONNET"} {
		if got := LookupKnownModelContextWindow(m); got == 0 {
			t.Errorf("case variant %q should match a known model", m)
		}
	}
}
