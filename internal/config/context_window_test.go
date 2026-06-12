package config

import "testing"

func TestInferContextWindow_KnownModels(t *testing.T) {
	tests := []struct {
		model string
		want  int
	}{
		// OpenAI exact matches (from catwalk data)
		{"gpt-4.1", 1047576},
		{"gpt-4.1-mini", 1047576},
		{"gpt-4.1-nano", 1047576},
		{"gpt-4o", 128000},
		{"gpt-4o-mini", 128000},
		{"gpt-4-turbo", 128000},
		{"gpt-4", 32768},
		{"o3", 200000},
		{"o3-mini", 200000},
		{"o4-mini", 200000},

		// OpenAI prefix matches (fall through to prefix heuristic)
		{"gpt-4.1-2025-04-14", 1_000_000},
		{"gpt-4o-2024-08-06", 128000},
		{"gpt-4-turbo-2024-04-09", 128000},

		// Anthropic Claude
		{"claude-sonnet-4-6", 1_000_000},
		{"claude-opus-4-7", 1_000_000},
		{"claude-sonnet-4-5-20250929", 200000},
		{"claude-opus-4-5-20251101", 200000},
		{"claude-opus-4-20250514", 200000},
		{"claude-sonnet-4-20250514", 200000},
		{"claude-haiku-4-5-20251001", 200000},

		// Google Gemini (prefix fallback when exact not found)
		{"gemini-2.5-pro", 1048576},
		{"gemini-2.5-flash", 1048576},
		{"gemini-2.0-flash", 1_000_000},
		{"gemini-1.5-pro", 2_000_000},
		{"gemini-1.5-flash", 1_000_000},

		// DeepSeek
		{"deepseek-chat", 128000},
		{"deepseek-reasoner", 128000},

		// Groq
		{"llama-3.3-70b-versatile", 128000},
		{"mixtral-8x7b-32768", 32768},

		// Zhipu GLM (from catwalk zai.json)
		{"glm-5", 204800},
		{"glm-5.1", 204800},
		{"glm-4-long", 1_000_000},

		// Moonshot / Kimi
		{"moonshot-v1-128k", 128000},
		{"moonshot-v1-32k", 32000},
		{"moonshot-v1-8k", 8000},
		{"kimi-k2", 262144},

		// XiaoMi MIMO
		{"MiMo-V2.5-Pro", 1_000_000},
		{"MiMo-V2.5", 1_000_000},
		{"MiMo-V2-Pro", 1_000_000},

		// Protocol fallback
		{"unknown-model", 128000},
	}

	for _, tt := range tests {
		got := inferContextWindow(tt.model, "openai")
		if got != tt.want {
			t.Errorf("inferContextWindow(%q) = %d, want %d", tt.model, got, tt.want)
		}
	}
}

func TestInferContextWindow_ProtocolFallback(t *testing.T) {
	if got := inferContextWindow("unknown", "anthropic"); got != 200000 {
		t.Errorf("anthropic fallback = %d, want 200000", got)
	}
	if got := inferContextWindow("unknown", "gemini"); got != 1_000_000 {
		t.Errorf("gemini fallback = %d, want 1000000", got)
	}
	if got := inferContextWindow("unknown", "openai"); got != 128000 {
		t.Errorf("openai fallback = %d, want 128000", got)
	}
}

func TestInferContextWindow_HintParsing(t *testing.T) {
	// Models with embedded context hints like "256k" or "1m"
	if got := inferContextWindow("my-model-256k", "openai"); got != 256000 {
		t.Errorf("hint 256k = %d, want 256000", got)
	}
	if got := inferContextWindow("model-1m-turbo", "openai"); got != 1_000_000 {
		t.Errorf("hint 1m = %d, want 1000000", got)
	}
	if got := inferContextWindow("model-128k-pro", "openai"); got != 128000 {
		t.Errorf("hint 128k = %d, want 128000", got)
	}
}

func TestInferVisionSupport_XiaoMiMIMO(t *testing.T) {
	if !inferVisionSupport("MiMo-V2.5", "openai") {
		t.Fatal("expected MiMo-V2.5 to infer vision support")
	}
	if !inferVisionSupport("MiMo-V2-Omni", "openai") {
		t.Fatal("expected MiMo-V2-Omni to infer vision support")
	}
	if inferVisionSupport("MiMo-V2.5-Pro", "openai") {
		t.Fatal("expected MiMo-V2.5-Pro to remain non-vision by default")
	}
}
