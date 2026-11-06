package provider

import (
	"strings"
)

var knownModelContextWindows = []struct {
	pattern string
	window  int
	exact   bool
}{

	{"claude-opus-4", 200_000, false},
	{"claude-sonnet-4", 200_000, false},
	{"claude-3-7-sonnet", 200_000, false},
	{"claude-3-5-sonnet", 200_000, false},
	{"claude-3-5-haiku", 200_000, false},
	{"claude-3-opus", 200_000, false},
	{"claude-3-haiku", 200_000, false},

	{"gpt-4o-mini", 128_000, false},
	{"gpt-4o", 128_000, false},
	{"gpt-4-turbo", 128_000, false},

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

	{"gemini-2.5-pro", 1_000_000, false},
	{"gemini-2.5-flash", 1_000_000, false},
	{"gemini-2.0-flash", 1_000_000, false},
	{"gemini-1.5-pro", 2_000_000, false},
	{"gemini-1.5-flash", 1_000_000, false},

	{"deepseek-r1", 64_000, false},
	{"deepseek-v3", 64_000, true},
	{"deepseek-chat", 64_000, true},
	{"deepseek-coder", 64_000, false},

	{"qwen-max", 32_000, false},
	{"qwen-plus", 131_072, false},
	{"qwen-turbo", 1_000_000, false},
	{"qwen2.5-72b", 131_072, false},
	{"qwen2.5-coder", 131_072, false},

	{"qwen3-coder", 0, false},
	{"qwen3", 131_072, false},

	{"glm-4-plus", 128_000, false},
	{"glm-4-air", 128_000, false},
	{"glm-4-flash", 128_000, false},
	{"glm-4-long", 1_000_000, false},
	{"glm-4v", 128_000, false},
	{"glm-4", 128_000, true},

	// GLM 4.5-4.7 and 5.x series (#5119). Values mirror the vendor-scoped
	// context-window table in internal/config/context_window.go (canonical
	// base-name entries). Prefix matching is order-sensitive: more specific
	// patterns must precede their general prefixes (e.g. glm-5.3-flash
	// before glm-5.3 before glm-5).
	//
	// #1287 contract preserved: bare glm-4.5, glm-4.5-air, glm-4.6, glm-4.7
	// and glm-5 deliberately have NO static entry - they are retired-family
	// names that must fall through to the live probe (see
	// lookupKnownRetiredFamilyHijack and TestIssue1287_GLM4ExactOnly), so
	// static entries here would re-hijack them and re-poison probe-cache
	// migration. Their variants below are not on that victim list.
	{"glm-4.5-flash", 131_072, false},
	{"glm-4.5v", 64_000, false},
	{"glm-4.6v", 128_000, false},
	{"glm-4.7-flashx", 200_000, false},
	{"glm-4.7-flash", 200_000, false},
	{"glm-4.7-free", 204_800, false},
	{"glm-5.3-flashx", 1_000_000, false},
	{"glm-5.3-flash", 1_000_000, false},
	{"glm-5.3-highspeed", 1_000_000, false},
	{"glm-5.3", 1_048_576, false},
	{"glm-5.2-fast", 1_000_000, false},
	{"glm-5.2-highspeed", 1_000_000, false},
	{"glm-5.2", 1_048_576, false},
	{"glm-5.1", 204_800, false},
	{"glm-5-2-260617", 1_000_000, false},
	{"glm-5-3-flash-260828", 1_000_000, false},
	{"glm-5v-turbo", 200_000, false},
	{"glm-5-turbo", 200_000, false},
	{"glm-5-free", 204_800, false},

	{"mistral-large", 128_000, false},
	{"mistral-medium", 32_000, false},
	{"mistral-small", 32_000, false},
	{"codestral", 256_000, false},
	{"mixtral", 32_000, false},

	{"llama-3.3-70b", 128_000, false},
	{"llama-3.1-405b", 128_000, false},
	{"llama-3.1-70b", 128_000, false},
	{"llama-3.1-8b", 128_000, false},

	{"yi-large", 32_000, false},

	{"command-r-plus", 128_000, false},
	{"command-r", 128_000, false},
}

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

			return entry.window
		}
	}
	return 0
}
