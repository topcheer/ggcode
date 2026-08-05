package cost

import (
	"strings"
	"testing"
)

func TestAnalyzeCache_NoPricing(t *testing.T) {
	// Tracker with unknown pricing - dollar amounts should be zero,
	// but token ratios should still be computed.
	tr := NewTracker("unknown-provider", "unknown-model", PricingTable{})

	// Record some cache activity.
	tr.Record(TokenUsage{
		InputTokens:  10000,
		OutputTokens: 5000,
		CacheRead:    30000,
		CacheWrite:   20000,
	})

	a := tr.AnalyzeCache()

	if a.HasPricing {
		t.Fatal("expected HasPricing=false for unknown provider")
	}
	if a.NetSavingsUSD != 0 {
		t.Fatalf("expected 0 NetSavingsUSD without pricing, got %f", a.NetSavingsUSD)
	}
	if a.CacheReadTokens != 30000 {
		t.Fatalf("expected 30000 cache read, got %d", a.CacheReadTokens)
	}
	if a.CacheWriteTokens != 20000 {
		t.Fatalf("expected 20000 cache write, got %d", a.CacheWriteTokens)
	}
	// CacheReadRatio = 30000 / (30000 + 10000) = 0.75
	if a.CacheReadRatio < 0.74 || a.CacheReadRatio > 0.76 {
		t.Fatalf("expected ~0.75 cache read ratio, got %f", a.CacheReadRatio)
	}
	// WriteToReadRatio = 20000 / 30000 = 0.667
	if a.WriteToReadRatio < 0.66 || a.WriteToReadRatio > 0.68 {
		t.Fatalf("expected ~0.667 write-to-read ratio, got %f", a.WriteToReadRatio)
	}
}

func TestAnalyzeCache_WithPricing_NetPositive(t *testing.T) {
	pricing := PricingTable{
		"anthropic": {
			"claude-sonnet": {
				Type:           PricingPerToken,
				InputPerM:      3.0,
				OutputPerM:     15.0,
				CacheReadPerM:  0.30, // 0.10x input
				CacheWritePerM: 3.75, // 1.25x input
			},
		},
	}
	tr := NewTracker("anthropic", "claude-sonnet", pricing)

	// High cache read, low cache write = good cache utilization.
	tr.Record(TokenUsage{
		InputTokens:  5000,
		OutputTokens: 2000,
		CacheRead:    100000,
		CacheWrite:   10000,
	})

	a := tr.AnalyzeCache()

	if !a.HasPricing {
		t.Fatal("expected HasPricing=true")
	}
	// GrossSavings = 100000 * (3.0 - 0.30) / 1e6 = 0.27
	if a.GrossSavingsUSD < 0.26 || a.GrossSavingsUSD > 0.28 {
		t.Fatalf("expected ~0.27 gross savings, got %f", a.GrossSavingsUSD)
	}
	// WritePremium = 10000 * (3.75 - 3.0) / 1e6 = 0.0075
	if a.CacheWritePremiumUSD < 0.007 || a.CacheWritePremiumUSD > 0.008 {
		t.Fatalf("expected ~0.0075 write premium, got %f", a.CacheWritePremiumUSD)
	}
	// NetSavings = 0.27 - 0.0075 = 0.2625
	if a.NetSavingsUSD < 0.25 || a.NetSavingsUSD > 0.28 {
		t.Fatalf("expected ~0.2625 net savings, got %f", a.NetSavingsUSD)
	}
	// Level should be excellent (net positive, low write/read ratio)
	if a.EfficiencyLevel() != CacheEffExcellent {
		t.Fatalf("expected CacheEffExcellent, got %d", a.EfficiencyLevel())
	}
}

func TestAnalyzeCache_Thrashing(t *testing.T) {
	pricing := PricingTable{
		"anthropic": {
			"claude-sonnet": {
				Type:           PricingPerToken,
				InputPerM:      3.0,
				OutputPerM:     15.0,
				CacheReadPerM:  0.30,
				CacheWritePerM: 3.75,
			},
		},
	}
	tr := NewTracker("anthropic", "claude-sonnet", pricing)

	// Write 20x more than read = thrashing.
	tr.Record(TokenUsage{
		InputTokens:  1000,
		OutputTokens: 500,
		CacheRead:    5000,
		CacheWrite:   100000,
	})

	a := tr.AnalyzeCache()

	// WriteToReadRatio = 100000 / 5000 = 20
	if a.WriteToReadRatio < 19 || a.WriteToReadRatio > 21 {
		t.Fatalf("expected ~20 write-to-read ratio, got %f", a.WriteToReadRatio)
	}
	// NetSavings should be negative (thrashing loses money).
	// GrossSavings = 5000 * (3.0 - 0.30) / 1e6 = 0.0135
	// WritePremium = 100000 * (3.75 - 3.0) / 1e6 = 0.075
	// NetSavings = 0.0135 - 0.075 = -0.0615
	if a.NetSavingsUSD >= 0 {
		t.Fatalf("expected negative net savings for thrashing, got %f", a.NetSavingsUSD)
	}
	if a.EfficiencyLevel() != CacheEffThrashing {
		t.Fatalf("expected CacheEffThrashing, got %d", a.EfficiencyLevel())
	}
}

func TestAnalyzeCache_NoActivity(t *testing.T) {
	tr := NewTracker("test", "model", PricingTable{})

	a := tr.AnalyzeCache()

	if a.EfficiencyLevel() != CacheEffNone {
		t.Fatalf("expected CacheEffNone for no activity, got %d", a.EfficiencyLevel())
	}
}

func TestAnalyzeCache_DefaultCacheRates(t *testing.T) {
	// Pricing table with per-token rates but NO explicit cache rates.
	// Should default to 0.10x read, 1.25x write.
	pricing := PricingTable{
		"test": {
			"model": {
				Type:       PricingPerToken,
				InputPerM:  10.0,
				OutputPerM: 30.0,
				// CacheReadPerM and CacheWritePerM intentionally zero.
			},
		},
	}
	tr := NewTracker("test", "model", pricing)

	tr.Record(TokenUsage{
		InputTokens: 1000,
		CacheRead:   100000,
		CacheWrite:  10000,
	})

	a := tr.AnalyzeCache()

	if !a.HasPricing {
		t.Fatal("expected HasPricing=true")
	}
	// Expected cache read rate = 10.0 * 0.10 = 1.0 per M
	// Expected cache write rate = 10.0 * 1.25 = 12.5 per M
	// GrossSavings = 100000 * (10.0 - 1.0) / 1e6 = 0.9
	if a.GrossSavingsUSD < 0.89 || a.GrossSavingsUSD > 0.91 {
		t.Fatalf("expected ~0.9 gross savings with default rates, got %f", a.GrossSavingsUSD)
	}
	// WritePremium = 10000 * (12.5 - 10.0) / 1e6 = 0.025
	if a.CacheWritePremiumUSD < 0.024 || a.CacheWritePremiumUSD > 0.026 {
		t.Fatalf("expected ~0.025 write premium with default rates, got %f", a.CacheWritePremiumUSD)
	}
}

func TestFormatCacheAnalysis_EmptyForNoActivity(t *testing.T) {
	a := CacheAnalysis{}
	if s := FormatCacheAnalysis(a); s != "" {
		t.Fatalf("expected empty string for no cache activity, got %q", s)
	}
}

func TestFormatCacheAnalysis_GoodCache(t *testing.T) {
	a := CacheAnalysis{
		CacheReadTokens:      234000,
		CacheWriteTokens:     89000,
		NonCachedInputTokens: 91000,
		CacheReadRatio:       0.72,
		NetSavingsUSD:        0.42,
		PercentSaved:         35,
		HasPricing:           true,
	}
	s := FormatCacheAnalysis(a)
	if !strings.Contains(s, "234,000") {
		t.Fatalf("expected '234,000' in output, got %q", s)
	}
	if !strings.Contains(s, "89,000") {
		t.Fatalf("expected '89,000' in output, got %q", s)
	}
	if !strings.Contains(s, "72%") {
		t.Fatalf("expected '72%%' in output, got %q", s)
	}
	if !strings.Contains(s, "35%") {
		t.Fatalf("expected '35%%' (percent saved) in output, got %q", s)
	}
}

func TestFormatCacheAnalysis_Thrashing(t *testing.T) {
	a := CacheAnalysis{
		CacheReadTokens:  5000,
		CacheWriteTokens: 67000,
		WriteToReadRatio: 13.4,
		NetSavingsUSD:    -0.15,
		PercentSaved:     -8,
		HasPricing:       true,
	}
	s := FormatCacheAnalysis(a)
	if !strings.Contains(s, "thrashing") {
		t.Fatalf("expected 'thrashing' in output, got %q", s)
	}
	if !strings.Contains(s, "13") {
		t.Fatalf("expected ratio '13' in output, got %q", s)
	}
}

func TestEfficiencyLevel_Marginal(t *testing.T) {
	// Ratio between 5 and 10 = marginal
	a := CacheAnalysis{
		CacheReadTokens:  10000,
		CacheWriteTokens: 70000,
		WriteToReadRatio: 7.0,
		HasPricing:       true,
		NetSavingsUSD:    0.01,
	}
	if a.EfficiencyLevel() != CacheEffMarginal {
		t.Fatalf("expected CacheEffMarginal for ratio 7, got %d", a.EfficiencyLevel())
	}
}

func TestEfficiencyLevel_GoodWithoutPricing(t *testing.T) {
	a := CacheAnalysis{
		CacheReadTokens:      80000,
		CacheWriteTokens:     20000,
		NonCachedInputTokens: 20000,
		CacheReadRatio:       0.8,
		WriteToReadRatio:     0.25,
		HasPricing:           false,
	}
	if a.EfficiencyLevel() != CacheEffGood {
		t.Fatalf("expected CacheEffGood for high read ratio without pricing, got %d", a.EfficiencyLevel())
	}
}
