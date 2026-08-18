package cost

// Regression tests for issue #687 (regression of #683): /cost all aggregates
// ignored per-session HasPricing and produced a false-precise grand total.
// The single-session path already displayed "(no pricing data)"; the
// aggregate must disclose how many summed sessions lack pricing.

import (
	"strings"
	"testing"
)

func TestIssue687_AggregateAllCosts_DisclosesSessionsWithoutPricing(t *testing.T) {
	m := NewManager(DefaultPricingTable(), t.TempDir())

	// Session with pricing: a known model.
	t1 := m.GetOrCreateTracker("s-priced", "anthropic", "claude-sonnet-4-5")
	t1.mu.Lock()
	t1.cost.InputTokens = 1000
	t1.cost.OutputTokens = 500
	t1.cost.TotalCostUSD = 0.01
	t1.cost.HasPricing = true
	t1.mu.Unlock()

	// Two sessions without pricing (unknown models).
	for _, id := range []string{"s-unpriced-1", "s-unpriced-2"} {
		t2 := m.GetOrCreateTracker(id, "weird-provider", "totally-unknown-model")
		t2.mu.Lock()
		t2.cost.InputTokens = 100
		t2.cost.HasPricing = false
		t2.mu.Unlock()
	}

	agg := m.AggregateAllCosts()
	if agg.SessionsWithoutPricing != 2 {
		t.Fatalf("expected 2 sessions without pricing, got %d", agg.SessionsWithoutPricing)
	}
	if agg.HasPricing {
		t.Fatal("aggregate HasPricing must be false when any session lacks pricing")
	}
	// Token totals still sum everything (tokens are known regardless of $).
	if agg.InputTokens != 1200 {
		t.Fatalf("input tokens must aggregate: got %d", agg.InputTokens)
	}
	if !strings.Contains(agg.Provider, "3 sessions") {
		t.Fatalf("provider label must carry session count: %q", agg.Provider)
	}
}

func TestIssue687_AggregateAllCosts_AllPriced_True(t *testing.T) {
	m := NewManager(DefaultPricingTable(), t.TempDir())
	t1 := m.GetOrCreateTracker("s1", "anthropic", "claude-sonnet-4-5")
	t1.mu.Lock()
	t1.cost.HasPricing = true
	t1.mu.Unlock()

	agg := m.AggregateAllCosts()
	if !agg.HasPricing {
		t.Fatal("all-priced aggregate must report HasPricing=true")
	}
	if agg.SessionsWithoutPricing != 0 {
		t.Fatalf("expected 0 unpriced, got %d", agg.SessionsWithoutPricing)
	}
}
