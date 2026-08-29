package provider

// Regression tests for GitHub issue #1287: over-broad known-model prefixes
// (same family as #782's gpt-4 hijack) adopted wrong legacy windows for
// newer variants, and context_probe Phase 1b short-circuits on known>0 so
// the wrong value was also written into the probe cache (sticky across
// sessions). Bare family names are now exact-only; qwen3-coder gets a
// 0-window blocker entry forcing the probe.

import "testing"

func TestIssue1287_GLM4ExactOnly(t *testing.T) {
	if got := LookupKnownModelContextWindow("glm-4"); got != 128_000 {
		t.Fatalf("bare glm-4 must keep its own window, got %d", got)
	}
	for _, victim := range []string{"glm-4.5", "glm-4.5-air", "glm-4.6", "glm-4.7", "glm-5"} {
		if got := LookupKnownModelContextWindow(victim); got != 0 {
			t.Fatalf("#1287: %s hijacked into legacy glm-4 window %d - must fall through to probe (0)", victim, got)
		}
	}
	// Listed glm-4 variants keep their entries (prefix entries unchanged).
	if got := LookupKnownModelContextWindow("glm-4-plus"); got != 128_000 {
		t.Fatalf("glm-4-plus lost its entry: %d", got)
	}
	if got := LookupKnownModelContextWindow("glm-4-long"); got != 1_000_000 {
		t.Fatalf("glm-4-long lost its entry: %d", got)
	}
}

func TestIssue1287_QwenCoderProbed(t *testing.T) {
	// Blocker entry: qwen3-coder-* is 256K+ in reality.
	if got := LookupKnownModelContextWindow("qwen3-coder-plus"); got != 0 {
		t.Fatalf("#1287: qwen3-coder-plus adopted %d - must be probed, not pinned to the 131K family entry", got)
	}
	// Non-coder qwen3 variants legitimately share the 131K family window.
	if got := LookupKnownModelContextWindow("qwen3-235b-a22b"); got != 131_072 {
		t.Fatalf("qwen3-235b-a22b must keep the family window, got %d", got)
	}
}

func TestIssue1287_DeepSeekExactOnly(t *testing.T) {
	for _, bare := range []string{"deepseek-chat", "deepseek-v3"} {
		if got := LookupKnownModelContextWindow(bare); got != 64_000 {
			t.Fatalf("bare %s must keep its window, got %d", bare, got)
		}
	}
	for _, victim := range []string{"deepseek-chat-v3.2", "deepseek-v3.2"} {
		if got := LookupKnownModelContextWindow(victim); got != 0 {
			t.Fatalf("#1287: %s hijacked into the 64K legacy window %d - must fall through to probe", victim, got)
		}
	}
	// deepseek-r1/coder prefixes unchanged.
	if got := LookupKnownModelContextWindow("deepseek-r1-0528"); got != 64_000 {
		t.Fatalf("deepseek-r1-0528 lost its family window: %d", got)
	}
}

func TestIssue1287_GPT4ExactMigration(t *testing.T) {
	// #782's in-code special case moved into the table's exact mechanism;
	// behavior must be identical.
	if got := LookupKnownModelContextWindow("gpt-4"); got != 8_192 {
		t.Fatalf("bare gpt-4 exact window changed: %d", got)
	}
	if got := LookupKnownModelContextWindow("gpt-4o-2024-08-06"); got != 128_000 {
		t.Fatalf("gpt-4o dated variant must keep prefix match: %d", got)
	}
}
