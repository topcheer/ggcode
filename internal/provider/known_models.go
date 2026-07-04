package provider

import (
	"strings"
)

// knownModelContextWindows maps model name patterns (lowercased, prefix match)
// to their known context window sizes in tokens.
//
// This table allows instant context window detection for well-known models,
// avoiding expensive API probing that sends padded messages to discover the
// limit empirically.
//
// Entries are checked in order; the first match wins. More specific patterns
// should come before less specific ones.
//
// Sources: official model documentation as of 2025-2026.
var knownModelContextWindows = []struct {
	pattern string
	window  int
}{
	// ── Claude (Anthropic) ──────────────────────────────────────────────
	{"claude-opus-4", 200_000},
	{"claude-sonnet-4", 200_000},
	{"claude-3-7-sonnet", 200_000},
	{"claude-3-5-sonnet", 200_000},
	{"claude-3-5-haiku", 200_000},
	{"claude-3-opus", 200_000},
	{"claude-3-haiku", 200_000},

	// ── GPT (OpenAI) ────────────────────────────────────────────────────
	{"gpt-4o-mini", 128_000},
	{"gpt-4o", 128_000},
	{"gpt-4-turbo", 128_000},
	{"gpt-4.1-mini", 1_000_000},
	{"gpt-4.1", 1_000_000},
	{"gpt-4.1-nano", 1_000_000},
	{"o3-mini", 200_000},
	{"o3", 200_000},
	{"o4-mini", 200_000},
	{"o1-mini", 128_000},
	{"o1-preview", 128_000},
	{"o1", 200_000},
	{"gpt-4", 8_192},
	{"gpt-3.5-turbo", 16_385},

	// ── Gemini (Google) ─────────────────────────────────────────────────
	{"gemini-2.5-pro", 1_000_000},
	{"gemini-2.5-flash", 1_000_000},
	{"gemini-2.0-flash", 1_000_000},
	{"gemini-1.5-pro", 2_000_000},
	{"gemini-1.5-flash", 1_000_000},

	// ── DeepSeek ────────────────────────────────────────────────────────
	{"deepseek-r1", 64_000},
	{"deepseek-v3", 64_000},
	{"deepseek-chat", 64_000},
	{"deepseek-coder", 64_000},

	// ── Qwen (Alibaba) ──────────────────────────────────────────────────
	{"qwen-max", 32_000},
	{"qwen-plus", 131_072},
	{"qwen-turbo", 1_000_000},
	{"qwen2.5-72b", 131_072},
	{"qwen2.5-coder", 131_072},
	{"qwen3", 131_072},

	// ── GLM (Zhipu AI) ──────────────────────────────────────────────────
	{"glm-4-plus", 128_000},
	{"glm-4-air", 128_000},
	{"glm-4-flash", 128_000},
	{"glm-4-long", 1_000_000},
	{"glm-4v", 128_000},
	{"glm-4", 128_000},

	// ── Mistral ─────────────────────────────────────────────────────────
	{"mistral-large", 128_000},
	{"mistral-medium", 32_000},
	{"mistral-small", 32_000},
	{"codestral", 256_000},
	{"mixtral", 32_000},

	// ── Llama (Meta) ────────────────────────────────────────────────────
	{"llama-3.3-70b", 128_000},
	{"llama-3.1-405b", 128_000},
	{"llama-3.1-70b", 128_000},
	{"llama-3.1-8b", 128_000},

	// ── Yi (01.AI) ──────────────────────────────────────────────────────
	{"yi-large", 32_000},

	// ── Command R+ (Cohere) ─────────────────────────────────────────────
	{"command-r-plus", 128_000},
	{"command-r", 128_000},
}

// LookupKnownModelContextWindow checks if the model name matches a known
// model and returns its documented context window size.
// Returns 0 if the model is not in the known table.
//
// Matching is case-insensitive prefix match: "gpt-4o-2024-08-06" matches
// the "gpt-4o" entry.
func LookupKnownModelContextWindow(model string) int {
	if model == "" {
		return 0
	}
	lower := strings.ToLower(model)
	for _, entry := range knownModelContextWindows {
		if strings.HasPrefix(lower, entry.pattern) {
			return entry.window
		}
	}
	return 0
}
