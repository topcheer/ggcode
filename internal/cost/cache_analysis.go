package cost

import (
	"fmt"
	"strings"
)

// CacheAnalysis computes the financial impact of prompt caching for a session.
//
// Prompt caching (Anthropic, Google Gemini context caching, OpenAI automatic
// caching) trades higher write cost for dramatically lower read cost:
//
//   - Cache write: typically 1.25x input price (paying a premium to cache)
//   - Cache read: typically 0.10x input price (90% savings on cached tokens)
//
// If the agent writes a lot to cache but rarely reads back (cache thrashing),
// caching actually LOSES money compared to not caching at all.
//
// This analysis answers: "Is prompt caching actually saving money?"
//
// Competitor mapping:
//   - Helicone: cache hit rate dashboard + savings calculator
//   - Braintrust: cost-per-call with cache attribution
//   - Claude Code: none (no cache analysis)
//   - Cursor: none
type CacheAnalysis struct {
	// CacheReadTokens is the total tokens served from cache.
	CacheReadTokens int64 `json:"cache_read_tokens"`
	// CacheWriteTokens is the total tokens written to cache.
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	// NonCachedInputTokens is the total uncached input tokens.
	NonCachedInputTokens int64 `json:"non_cached_input_tokens"`

	// GrossSavingsUSD is the savings from cache reads vs paying full input price.
	// = CacheReadTokens * (InputPerM - CacheReadPerM) / 1e6
	GrossSavingsUSD float64 `json:"gross_savings_usd"`
	// CacheWritePremiumUSD is the extra cost paid for writing to cache vs input price.
	// = CacheWriteTokens * (CacheWritePerM - InputPerM) / 1e6
	CacheWritePremiumUSD float64 `json:"cache_write_premium_usd"`
	// NetSavingsUSD is GrossSavings minus WritePremium. Negative = caching loses money.
	NetSavingsUSD float64 `json:"net_savings_usd"`

	// CacheReadRatio is the fraction of cached reads relative to total reads.
	// = CacheRead / (CacheRead + NonCachedInput). High = good cache utilization.
	CacheReadRatio float64 `json:"cache_read_ratio"`
	// WriteToReadRatio is CacheWrite / CacheRead. Values > 5 indicate thrashing
	// (writing far more to cache than reading back).
	WriteToReadRatio float64 `json:"write_to_read_ratio"`

	// EffectiveCostWithoutCache is what the session would cost with zero caching
	// (all tokens at input price). Useful for showing "% saved by caching".
	EffectiveCostWithoutCache float64 `json:"cost_without_cache_usd"`
	// PercentSaved is (CostWithoutCache - ActualCost) / CostWithoutCache * 100.
	PercentSaved float64 `json:"percent_saved"`

	// HasPricing indicates whether pricing data was available for the computation.
	// When false, only token ratios are meaningful (dollar amounts are zero).
	HasPricing bool `json:"has_pricing"`
}

// AnalyzeCache computes the cache efficiency analysis for a tracker's session.
// Returns a CacheAnalysis with dollar savings, ratios, and efficiency metrics.
//
// If no pricing data is available (HasPricing=false), only token-based ratios
// (CacheReadRatio, WriteToReadRatio) are meaningful; dollar amounts are zero.
func (t *Tracker) AnalyzeCache() CacheAnalysis {
	t.mu.Lock()
	defer t.mu.Unlock()
	return analyzeCacheLocked(t.cost, t.pricing)
}

// AnalyzeCacheFromSessionCost computes cache efficiency from a SessionCost
// snapshot and pricing table, without needing a live Tracker. This is used
// by display layers (e.g., /cost command) that only have aggregated data.
func AnalyzeCacheFromSessionCost(sc SessionCost, pricing PricingTable) CacheAnalysis {
	return analyzeCacheLocked(sc, pricing)
}

func analyzeCacheLocked(sc SessionCost, pricing PricingTable) CacheAnalysis {
	a := CacheAnalysis{
		CacheReadTokens:      sc.CacheReadTokens,
		CacheWriteTokens:     sc.CacheWriteTokens,
		NonCachedInputTokens: sc.InputTokens,
	}

	// Compute token-based ratios (meaningful even without pricing).
	totalRead := sc.CacheReadTokens + sc.InputTokens
	if totalRead > 0 {
		a.CacheReadRatio = float64(sc.CacheReadTokens) / float64(totalRead)
	}
	if sc.CacheReadTokens > 0 {
		a.WriteToReadRatio = float64(sc.CacheWriteTokens) / float64(sc.CacheReadTokens)
	}

	rate, ok := pricing.Get(sc.Provider, sc.Model)
	if !ok || !rate.IsMetered() {
		a.HasPricing = false
		return a
	}
	a.HasPricing = true

	// #529: single source of truth for effective cache rates, shared with the
	// Tracker (recalculate / recordAgentLocked) so both layers agree even when
	// the user's merged pricing table omits explicit cache fields.
	cacheReadPerM, cacheWritePerM := effectiveCacheRates(rate)

	// Gross savings: what we saved by reading from cache instead of paying input price.
	a.GrossSavingsUSD = float64(sc.CacheReadTokens) * (rate.InputPerM - cacheReadPerM) / 1e6

	// Write premium: extra cost paid for writing to cache instead of input price.
	a.CacheWritePremiumUSD = float64(sc.CacheWriteTokens) * (cacheWritePerM - rate.InputPerM) / 1e6

	a.NetSavingsUSD = a.GrossSavingsUSD - a.CacheWritePremiumUSD

	// #529: numerator (TotalCostUSD) includes output tokens, so the baseline
	// must too — otherwise output-heavy sessions produce absurd deep-negative
	// percentages (e.g. -633%). Baseline = same tokens priced as if no caching
	// existed (all input-shaped tokens at input price + output at output price).
	a.EffectiveCostWithoutCache = float64(sc.InputTokens+sc.CacheReadTokens+sc.CacheWriteTokens)*rate.InputPerM/1e6 +
		float64(sc.OutputTokens)*rate.OutputPerM/1e6

	if a.EffectiveCostWithoutCache > 0 {
		a.PercentSaved = (a.EffectiveCostWithoutCache - sc.TotalCostUSD) / a.EffectiveCostWithoutCache * 100
	}

	return a
}

// effectiveCacheRates returns the cache read/write per-million rates for a
// metered model, falling back to the industry-standard Anthropic prompt
// caching ratios (cache_read = 0.10x input, cache_write = 1.25x input) when
// the pricing table omits explicit cache fields.
//
// #529: shared by analyzeCacheLocked and Tracker.recalculate/recordAgentLocked
// so the tracker and the analysis layer can never disagree on cache pricing
// (previously the tracker billed cache tokens at 0 when fields were unset
// while the analysis layer assumed the 0.10x/1.25x fallback).
func effectiveCacheRates(rate ModelRate) (cacheReadPerM, cacheWritePerM float64) {
	// #559 (Bug C): only fall back to the industry-standard heuristics when
	// the field is genuinely ABSENT. An explicit `cache_read_per_m: 0` (free
	// cache reads) must stay 0 — previously it was treated as missing and
	// billed at 0.10x input ($18 → $123, 6.8x).
	cacheReadPerM = rate.InputPerM * 0.10
	if rate.CacheReadSet || rate.CacheReadPerM > 0 {
		cacheReadPerM = rate.CacheReadPerM
	}
	cacheWritePerM = rate.InputPerM * 1.25
	if rate.CacheWriteSet || rate.CacheWritePerM > 0 {
		cacheWritePerM = rate.CacheWritePerM
	}
	return cacheReadPerM, cacheWritePerM
}

// CacheEfficiencyLevel categorizes cache performance.
type CacheEfficiencyLevel int

const (
	// CacheEffNone: no cache activity (zero reads and writes).
	CacheEffNone CacheEfficiencyLevel = iota
	// CacheEffExcellent: net savings > 0 and write-to-read ratio < 3.
	CacheEffExcellent
	// CacheEffGood: net savings > 0 but moderate thrashing (ratio 3-5).
	CacheEffGood
	// CacheEffMarginal: net savings near zero or borderline (ratio 5-10).
	CacheEffMarginal
	// CacheEffThrashing: cache writes far exceed reads (ratio > 10), caching loses money.
	CacheEffThrashing
)

// EfficiencyLevel classifies the cache analysis into a coarse category.
// Used by the agent gate to decide whether to warn.
func (a CacheAnalysis) EfficiencyLevel() CacheEfficiencyLevel {
	if a.CacheReadTokens == 0 && a.CacheWriteTokens == 0 {
		return CacheEffNone
	}

	// If write-to-read ratio is very high, we're writing to cache but rarely
	// reading back — this is cache thrashing and wastes the write premium.
	// #529: write-only caching (read == 0, write > 0) is the degenerate extreme
	// of the same curve — the entire write premium is lost with zero payback —
	// so it is Thrashing, not Marginal.
	if a.CacheWriteTokens > 0 && a.CacheReadTokens == 0 {
		return CacheEffThrashing
	}
	if a.CacheWriteTokens > 0 && a.CacheReadTokens > 0 {
		ratio := float64(a.CacheWriteTokens) / float64(a.CacheReadTokens)
		if ratio > 10 {
			return CacheEffThrashing
		}
		if ratio > 5 {
			return CacheEffMarginal
		}
	}

	if a.HasPricing {
		if a.NetSavingsUSD > 0 {
			if a.WriteToReadRatio > 0 && a.WriteToReadRatio < 3 {
				return CacheEffExcellent
			}
			return CacheEffGood
		}
		return CacheEffMarginal
	}

	// Without pricing, judge by read ratio alone.
	if a.CacheReadRatio > 0.3 {
		return CacheEffGood
	}
	return CacheEffMarginal
}

// FormatCacheAnalysis returns a human-readable cache efficiency report.
//
// Example output:
//
//	Cache: 234K read  89K write  hit-ratio: 72%  savings: $0.42 (35% off)
//	Cache: 5K read  67K write  hit-ratio: 7%  ⚠ thrashing (writes 13x reads)
func FormatCacheAnalysis(a CacheAnalysis) string {
	if a.CacheReadTokens == 0 && a.CacheWriteTokens == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("  Cache: ")
	b.WriteString(FormatTokens(a.CacheReadTokens))
	b.WriteString(" read  ")
	b.WriteString(FormatTokens(a.CacheWriteTokens))
	b.WriteString(" write  ")

	if a.CacheReadTokens+a.NonCachedInputTokens > 0 {
		b.WriteString(fmt.Sprintf("hit-ratio: %.0f%%  ", a.CacheReadRatio*100))
	}

	if a.HasPricing {
		if a.NetSavingsUSD >= 0 {
			b.WriteString(fmt.Sprintf("savings: %s (%.0f%% off)", FormatCost(a.NetSavingsUSD), a.PercentSaved))
		} else {
			// #683: PercentSaved is already negative on the loss branch, so
			// "(-%.0f%%)" produced "--633%". Use the absolute value and let the
			// explicit "-" carry the direction.
			pct := a.PercentSaved
			if pct < 0 {
				pct = -pct
			}
			b.WriteString(fmt.Sprintf("net loss: %s (-%.0f%%)", FormatCost(-a.NetSavingsUSD), pct))
		}
	}

	switch a.EfficiencyLevel() {
	case CacheEffThrashing:
		b.WriteString(fmt.Sprintf("  Warning: thrashing (writes %.1fx reads)", a.WriteToReadRatio))
	case CacheEffMarginal:
		if a.WriteToReadRatio > 5 {
			b.WriteString(fmt.Sprintf("  Note: high write ratio (%.1fx reads)", a.WriteToReadRatio))
		}
	}

	return b.String()
}
