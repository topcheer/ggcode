package cost

import (
	"math"
	"testing"
)

// TestPercentSavedBillingBasisConsistency (#528→#529 Bug A): with
// input=1M, cache_read=0.5M, output=2M, InputPerM=3, OutputPerM=15 the old
// baseline excluded output tokens (4.50) while the numerator's TotalCostUSD
// included them (33.00), yielding an absurd PercentSaved of -633.33%.
// Both sides must use the same billing basis: baseline = all input-shaped
// tokens at input price + output at output price (34.50), giving ~+3.9%.
func TestPercentSavedBillingBasisConsistency(t *testing.T) {
	sc := SessionCost{
		Provider:        "anthropic",
		Model:           "claude",
		InputTokens:     1_000_000,
		OutputTokens:    2_000_000,
		CacheReadTokens: 500_000,
	}
	pricing := PricingTable{"anthropic": {"claude": ModelRate{
		InputPerM: 3, OutputPerM: 15,
		CacheReadPerM: 0.3, CacheWritePerM: 3.75,
		Type: PricingPerToken,
	}}}
	sc.TotalCostUSD = 1_000_000*3/1e6 + 500_000*0.3/1e6 + 2_000_000*15/1e6 // 33.00

	a := AnalyzeCacheFromSessionCost(sc, pricing)

	wantBaseline := (1_000_000+500_000)*3.0/1e6 + 2_000_000*15.0/1e6 // 34.50
	if math.Abs(a.EffectiveCostWithoutCache-wantBaseline) > 1e-9 {
		t.Errorf("EffectiveCostWithoutCache = %.4f, want %.4f (output included in baseline)", a.EffectiveCostWithoutCache, wantBaseline)
	}
	if a.PercentSaved < 0 || a.PercentSaved > 10 {
		t.Errorf("PercentSaved = %.2f%%, want a sane small positive value (~3.9%%), not -633%%", a.PercentSaved)
	}
	if math.Abs(a.PercentSaved-3.9) > 0.2 {
		t.Errorf("PercentSaved = %.2f%%, want ~3.9%%", a.PercentSaved)
	}
}

// TestTrackerAndAnalysisCacheRateFallbackAgree (#529 Bug B): when the user's
// merged pricing table omits cache fields, the tracker previously billed cache
// tokens at zero while analyzeCacheLocked assumed 0.10x/1.25x input. Both
// layers must now use the same shared fallback so the same session cannot
// produce two different totals.
func TestTrackerAndAnalysisCacheRateFallbackAgree(t *testing.T) {
	pricing := PricingTable{"p": {"m": ModelRate{
		InputPerM: 3, OutputPerM: 15, // cache fields deliberately unset
		Type: PricingPerToken,
	}}}
	tr := NewTracker("p", "m", pricing)
	tr.Record(TokenUsage{InputTokens: 100_000, CacheRead: 50_000, CacheWrite: 10_000})

	sc := tr.SessionCost()
	a := AnalyzeCacheFromSessionCost(sc, pricing)

	// Recompute the session total with the analysis layer's rates.
	cacheReadPerM, cacheWritePerM := effectiveCacheRates(pricing.MustGet("p", "m"))
	want := 100_000*3.0/1e6 + 50_000*cacheReadPerM/1e6 + 10_000*cacheWritePerM/1e6
	if math.Abs(sc.TotalCostUSD-want) > 1e-9 {
		t.Errorf("tracker TotalCostUSD = %.6f, want %.6f (shared cache-rate fallback)", sc.TotalCostUSD, want)
	}
	if math.Abs(cacheReadPerM-0.3) > 1e-9 || math.Abs(cacheWritePerM-3.75) > 1e-9 {
		t.Errorf("effectiveCacheRates fallback = (%v, %v), want (0.3, 3.75)", cacheReadPerM, cacheWritePerM)
	}
	if a.HasPricing && math.Abs(sc.TotalCostUSD-0) < 1e-9 {
		t.Error("cache tokens must not be billed at zero when cache fields are unset")
	}
}

// TestWriteOnlyCacheClassifiedThrashing (#529 Bug C): write>0 / read=0 loses
// the entire write premium with zero payback — the degenerate extreme of the
// thrashing curve — and must be CacheEffThrashing, not Marginal.
func TestWriteOnlyCacheClassifiedThrashing(t *testing.T) {
	sc := SessionCost{
		Provider:         "p",
		Model:            "m",
		InputTokens:      10_000,
		CacheWriteTokens: 80_000,
	}
	pricing := PricingTable{"p": {"m": ModelRate{
		InputPerM: 3, OutputPerM: 15,
		CacheReadPerM: 0.3, CacheWritePerM: 3.75,
		Type: PricingPerToken,
	}}}
	a := AnalyzeCacheFromSessionCost(sc, pricing)
	if got := a.EfficiencyLevel(); got != CacheEffThrashing {
		t.Errorf("write-only cache EfficiencyLevel() = %d, want CacheEffThrashing", got)
	}
}

// MustGet is a test helper returning the pricing entry or panicking.
func (t PricingTable) MustGet(provider, model string) ModelRate {
	rate, ok := t.Get(provider, model)
	if !ok {
		panic("pricing entry not found: " + provider + "/" + model)
	}
	return rate
}
