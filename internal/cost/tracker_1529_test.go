package cost

import (
	"testing"
)

// #1529 probe: OpenAI-compat subset semantics (InputTokens CONTAINS
// CacheRead) must not double-count at the cost write boundary.
func TestTrackerRecordSubsetNormalization(t *testing.T) {
	tr := NewTracker("openai", "gpt-test", DefaultPricingTable())
	// Subset form: PromptTokensTotal=1000 (all), CacheRead=800,
	// InputTokens as stored = 1000 (subset semantics).
	tr.Record(TokenUsage{
		InputTokens:       1000,
		PromptTokensTotal: 1000,
		CacheRead:         800,
		OutputTokens:      50,
	})
	sc := tr.SessionCost()
	if sc.InputTokens != 200 {
		t.Fatalf("#1529: subset InputTokens not normalized to uncached remainder: got %d, want 200", sc.InputTokens)
	}
	if sc.CacheReadTokens != 800 {
		t.Fatalf("cache read mutated: %d", sc.CacheReadTokens)
	}
}

// Disjoint semantics (Anthropic): InputTokens EXCLUDES cache reads - the
// normalization must be a no-op.
func TestTrackerRecordDisjointUntouched(t *testing.T) {
	tr := NewTracker("anthropic", "claude-test", DefaultPricingTable())
	tr.Record(TokenUsage{
		InputTokens:  200,
		CacheRead:    800,
		OutputTokens: 50,
	})
	sc := tr.SessionCost()
	if sc.InputTokens != 200 {
		t.Fatalf("disjoint InputTokens must stay 200, got %d", sc.InputTokens)
	}
	if sc.CacheReadTokens != 800 {
		t.Fatalf("cache read mutated: %d", sc.CacheReadTokens)
	}
}
