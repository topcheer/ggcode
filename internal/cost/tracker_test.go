package cost

import (
	"testing"
)

func TestTrackerRecord(t *testing.T) {
	pricing := DefaultPricingTable()
	tr := NewTracker("anthropic", "claude-sonnet-4-20250514", pricing)

	tr.Record(TokenUsage{InputTokens: 1000, OutputTokens: 500})
	sc := tr.SessionCost()

	if sc.InputTokens != 1000 {
		t.Errorf("input tokens = %d, want 1000", sc.InputTokens)
	}
	if sc.OutputTokens != 500 {
		t.Errorf("output tokens = %d, want 500", sc.OutputTokens)
	}
	if sc.TotalCostUSD == 0 {
		t.Error("expected non-zero cost")
	}

	// Verify calculation: 1000*3.0/1e6 + 500*15.0/1e6
	expected := 1000*3.0/1e6 + 500*15.0/1e6
	if sc.TotalCostUSD < expected-0.0001 || sc.TotalCostUSD > expected+0.0001 {
		t.Errorf("cost = %f, want %f", sc.TotalCostUSD, expected)
	}
}

func TestTrackerMultipleRecords(t *testing.T) {
	pricing := DefaultPricingTable()
	tr := NewTracker("anthropic", "claude-sonnet-4-20250514", pricing)

	tr.Record(TokenUsage{InputTokens: 1000, OutputTokens: 500})
	tr.Record(TokenUsage{InputTokens: 2000, OutputTokens: 1000})

	sc := tr.SessionCost()
	if sc.InputTokens != 3000 {
		t.Errorf("input tokens = %d, want 3000", sc.InputTokens)
	}
	if sc.OutputTokens != 1500 {
		t.Errorf("output tokens = %d, want 1500", sc.OutputTokens)
	}
}

func TestTrackerUnknownModel(t *testing.T) {
	pricing := PricingTable{}
	tr := NewTracker("unknown", "unknown-model", pricing)

	tr.Record(TokenUsage{InputTokens: 1000, OutputTokens: 500})
	sc := tr.SessionCost()

	if sc.TotalCostUSD != 0 {
		t.Errorf("expected zero cost for unknown model, got %f", sc.TotalCostUSD)
	}
}

func TestManagerAllCosts(t *testing.T) {
	pricing := DefaultPricingTable()
	mgr := NewManager(pricing, "")

	mgr.GetOrCreateTracker("s1", "anthropic", "claude-sonnet-4-20250514").
		Record(TokenUsage{InputTokens: 1000, OutputTokens: 500})
	mgr.GetOrCreateTracker("s2", "openai", "gpt-4o").
		Record(TokenUsage{InputTokens: 2000, OutputTokens: 1000})

	costs := mgr.AllCosts()
	if len(costs) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(costs))
	}
	// Should be sorted by cost descending
	if costs[0].TotalCostUSD < costs[1].TotalCostUSD {
		t.Error("expected descending order by cost")
	}
}

func TestManagerTotalCost(t *testing.T) {
	pricing := DefaultPricingTable()
	mgr := NewManager(pricing, "")

	mgr.GetOrCreateTracker("s1", "anthropic", "claude-sonnet-4-20250514").
		Record(TokenUsage{InputTokens: 1000000, OutputTokens: 0})
	mgr.GetOrCreateTracker("s2", "anthropic", "claude-sonnet-4-20250514").
		Record(TokenUsage{InputTokens: 1000000, OutputTokens: 0})

	total := mgr.TotalCost()
	if total < 5.99 || total > 6.01 {
		t.Errorf("total cost = %f, want ~6.0", total)
	}
}

func TestPricingMerge(t *testing.T) {
	base := DefaultPricingTable()
	override := PricingTable{
		"anthropic": {
			"claude-sonnet-4-20250514": {InputPerM: 1.0, OutputPerM: 5.0},
		},
	}
	merged := base.Merge(override)

	rate, ok := merged.Get("anthropic", "claude-sonnet-4-20250514")
	if !ok {
		t.Fatal("expected claude-sonnet to exist in merged table")
	}
	if rate.InputPerM != 1.0 {
		t.Errorf("input price = %f, want 1.0", rate.InputPerM)
	}
	// Original claude-opus should still be there
	_, ok = merged.Get("anthropic", "claude-opus-4-20250514")
	if !ok {
		t.Error("expected claude-opus to still exist after merge")
	}
}

func TestFormatCost(t *testing.T) {
	tests := []struct {
		usd  float64
		want string
	}{
		{0.001, "$0.0010"},
		{0.5, "$0.50"},
		{1.23, "$1.23"},
		{123.456, "$123.46"},
	}
	for _, tt := range tests {
		got := FormatCost(tt.usd)
		if got != tt.want {
			t.Errorf("FormatCost(%f) = %q, want %q", tt.usd, got, tt.want)
		}
	}
}

func TestFormatTokens(t *testing.T) {
	if got := FormatTokens(500); got != "500" {
		t.Errorf("got %q", got)
	}
	if got := FormatTokens(1500); got != "1,500" {
		t.Errorf("got %q", got)
	}
}
