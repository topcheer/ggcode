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
	// exact marks bare family-name entries that must match the FULL model
	// name (#782/#1287). As prefixes they hijacked every unlisted newer
	// variant into the legacy window (glm-4.5+/qwen3-coder/deepseek-v3.2
	// got 2x-underestimated), and context_probe Phase 1b short-circuits on
	// known>0 — the wrong value also got cached. Unlisted variants fall
	// through to the background probe, which measures the real window.
	exact bool
}{
	// ── Claude (Anthropic) ──────────────────────────────────────────────
	{"claude-opus-4", 200_000, false},
	{"claude-sonnet-4", 200_000, false},
	{"claude-3-7-sonnet", 200_000, false},
	{"claude-3-5-sonnet", 200_000, false},
	{"claude-3-5-haiku", 200_000, false},
	{"claude-3-opus", 200_000, false},
	{"claude-3-haiku", 200_000, false},

	// ── GPT (OpenAI) ────────────────────────────────────────────────────
	{"gpt-4o-mini", 128_000, false},
	{"gpt-4o", 128_000, false},
	{"gpt-4-turbo", 128_000, false},
	// #782: bare gpt-4 exact-only (migrated from the in-code special case
	// into the table's exact mechanism in #1287): unlisted gpt-4.x variants
	// (128K-1M real windows) must fall through to the probe, never inherit
	// the legacy 8K window.
	{"gpt-4", 8_192, true},
	{"gpt-4.1-mini", 1_000_000, false},
	{"gpt-4.1", 1_000_000, false},
	{"gpt-4.1-nano", 1_000_000, false},
	{"o3-mini", 200_000, false},
	{"o3", 200_000, false},
	{"o4-mini", 200_000, false},
	{"o1-mini", 128_000, false},
	{"o1-preview", 128_000, false},
	{"o1", 200_000, false},
	{"gpt-3.5-turbo", 16_385, false},

	// ── Gemini (Google) ─────────────────────────────────────────────────
	{"gemini-2.5-pro", 1_000_000, false},
	{"gemini-2.5-flash", 1_000_000, false},
	{"gemini-2.0-flash", 1_000_000, false},
	{"gemini-1.5-pro", 2_000_000, false},
	{"gemini-1.5-flash", 1_000_000, false},

	// ── DeepSeek ────────────────────────────────────────────────────────
	{"deepseek-r1", 64_000, false},
	{"deepseek-v3", 64_000, true},   // exact: v3.2 is 128K, probe it
	{"deepseek-chat", 64_000, true}, // exact: -v3.2 is 128K, probe it
	{"deepseek-coder", 64_000, false},

	// ── Qwen (Alibaba) ──────────────────────────────────────────────────
	{"qwen-max", 32_000, false},
	{"qwen-plus", 131_072, false},
	{"qwen-turbo", 1_000_000, false},
	{"qwen2.5-72b", 131_072, false},
	{"qwen2.5-coder", 131_072, false},
	// #1287 blocker entry: qwen3-coder-* has 256K+ (vs the family's 131K);
	// a 0-window match short-circuits the scan so the lookup returns 0 and
	// the context probe measures the real window instead of inheriting a
	// 2x-underestimate. Must stay BEFORE the qwen3 prefix entry.
	{"qwen3-coder", 0, false},
	{"qwen3", 131_072, false}, // prefix: qwen3-235b etc. share 131K

	// ── GLM (Zhipu AI) ──────────────────────────────────────────────────
	{"glm-4-plus", 128_000, false},
	{"glm-4-air", 128_000, false},
	{"glm-4-flash", 128_000, false},
	{"glm-4-long", 1_000_000, false},
	{"glm-4v", 128_000, false},
	{"glm-4", 128_000, true}, // exact: glm-4.5+ are 128K-200K, probe them

	// ── Mistral ─────────────────────────────────────────────────────────
	{"mistral-large", 128_000, false},
	{"mistral-medium", 32_000, false},
	{"mistral-small", 32_000, false},
	{"codestral", 256_000, false},
	{"mixtral", 32_000, false},

	// ── Llama (Meta) ────────────────────────────────────────────────────
	{"llama-3.3-70b", 128_000, false},
	{"llama-3.1-405b", 128_000, false},
	{"llama-3.1-70b", 128_000, false},
	{"llama-3.1-8b", 128_000, false},

	// ── Yi (01.AI) ──────────────────────────────────────────────────────
	{"yi-large", 32_000, false},

	// ── Command R+ (Cohere) ─────────────────────────────────────────────
	{"command-r-plus", 128_000, false},
	{"command-r", 128_000, false},
}

// LookupKnownModelContextWindow checks if the model name matches a known
// model and returns its documented context window size.
// Returns 0 if the model is not in the known table.
//
// Matching is case-insensitive. Prefix entries match "gpt-4o-2024-08-06"
// against "gpt-4o"; entries marked exact match the full name only — bare
// family names whose newer suffix variants have different windows (#782,
// #1287) so unlisted variants reach the probe path instead of inheriting
// a stale legacy window.
func LookupKnownModelContextWindow(model string) int {
	if model == "" {
		return 0
	}
	lower := strings.ToLower(model)
	for _, entry := range knownModelContextWindows {
		if entry.exact {
			if lower == entry.pattern {
				return entry.window
			}
			continue
		}
		if strings.HasPrefix(lower, entry.pattern) {
			// 0-window blocker entries (#1287): matched on purpose to stop
			// a wider family prefix from adopting a wrong legacy window.
			// Callers treat 0 as unknown and fall through to the probe.
			return entry.window
		}
	}
	return 0
}
