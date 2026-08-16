package cost

import (
	"encoding/json"
	"testing"
)

// ---- Bug A: bidirectional prefix mispricing ----

// #559 A: querying glm-4.5 (paid) must not hit glm-4.5-air (free) — the old
// bidirectional HasPrefix matched because the QUERY is a prefix of the KEY.
func TestIssue559A_NoReversePrefixMatch(t *testing.T) {
	pt := PricingTable{
		"zhipu": {
			"glm-4.5-air": {Type: PricingFree, Plan: "Zhipu Free Tier"},
		},
	}
	if _, ok := pt.Get("zhipu", "glm-4.5"); ok {
		t.Error("glm-4.5 must not match glm-4.5-air via reverse prefix (Bug A): $11 showed as $0")
	}
	// Forward direction still works: glm-4.5-air-experimental starts with the key.
	if rate, ok := pt.Get("zhipu", "glm-4.5-air-preview"); !ok || rate.Type != PricingFree {
		t.Errorf("forward prefix match broken: ok=%v rate=%+v", ok, rate)
	}
}

// #559 A: exact and forward-prefix behavior preserved (longest key wins).
func TestIssue559A_ForwardPrefixLongestWins(t *testing.T) {
	pt := PricingTable{
		"anthropic": {
			"claude-":     {Type: PricingPerToken, InputPerM: 1},
			"claude-opus": {Type: PricingPerToken, InputPerM: 15},
		},
	}
	rate, ok := pt.Get("anthropic", "claude-opus-4.5")
	if !ok || rate.InputPerM != 15 {
		t.Errorf("longest forward prefix should win: ok=%v rate=%+v", ok, rate)
	}
}

// ---- Bug B: over-broad suffix match ----

// #559 B: "my-proxy-o3" must not hit the "o3" subscription rate.
func TestIssue559B_SuffixRequiresBoundary(t *testing.T) {
	pt := PricingTable{
		"github-copilot": {
			"o3": {Type: PricingSubscription, Plan: "GitHub Copilot"},
		},
	}
	if _, ok := pt.Get("github-copilot", "my-proxy-o3"); ok {
		t.Error("my-proxy-o3 must not match o3 subscription (Bug B): $180 shown as included")
	}
	// Hyphenated longer names must not inherit a suffix-key rate either.
	if _, ok := pt.Get("github-copilot", "x-o3"); ok {
		t.Error("x-o3 must not match o3 via hyphen boundary (hyphens are not a hard boundary)")
	}
	// Path boundary still matches (e.g. "anthropic/o3").
	if rate, ok := pt.Get("github-copilot", "anthropic/o3"); !ok || rate.Type != PricingSubscription {
		t.Errorf("path-boundary suffix match broken: ok=%v rate=%+v", ok, rate)
	}
	// Exact match obviously still works.
	if rate, ok := pt.Get("github-copilot", "o3"); !ok || rate.Type != PricingSubscription {
		t.Errorf("exact match broken: ok=%v rate=%+v", ok, rate)
	}
}

// ---- Bug C: explicit cache_read_per_m: 0 (free) vs missing ----

// #559 C: explicit cache_read_per_m: 0 must be treated as FREE, not missing
// (probe: $18 reported as $123 via 0.10x fallback, 6.8x).
func TestIssue559C_ExplicitZeroCacheReadIsFree(t *testing.T) {
	// Simulate a user pricing entry that sets cache_read_per_m explicitly to 0.
	raw := `{"input_per_m": 3.0, "output_per_m": 15.0, "cache_read_per_m": 0, "type": "per_token"}`
	var rate ModelRate
	if err := json.Unmarshal([]byte(raw), &rate); err != nil {
		t.Fatal(err)
	}
	cr, _ := effectiveCacheRates(rate)
	if cr != 0 {
		t.Errorf("explicit cache_read_per_m:0 billed at %v (Bug C: treated as missing → 0.10x)", cr)
	}
}

// #559 C: absent cache fields still fall back to 0.10x / 1.25x heuristics.
func TestIssue559C_MissingCacheFieldsStillFallBack(t *testing.T) {
	raw := `{"input_per_m": 3.0, "output_per_m": 15.0, "type": "per_token"}`
	var rate ModelRate
	if err := json.Unmarshal([]byte(raw), &rate); err != nil {
		t.Fatal(err)
	}
	cr, cw := effectiveCacheRates(rate)
	if !nearlyEqual(cr, 0.3) {
		t.Errorf("missing cache_read fallback = %v, want 0.3", cr)
	}
	if !nearlyEqual(cw, 3.75) {
		t.Errorf("missing cache_write fallback = %v, want 3.75", cw)
	}
}

// #559 C: nonzero explicit cache rates respected.
func TestIssue559C_ExplicitNonzeroCacheRates(t *testing.T) {
	raw := `{"input_per_m": 3.0, "output_per_m": 15.0, "cache_read_per_m": 0.6, "cache_write_per_m": 4.5, "type": "per_token"}`
	var rate ModelRate
	if err := json.Unmarshal([]byte(raw), &rate); err != nil {
		t.Fatal(err)
	}
	cr, cw := effectiveCacheRates(rate)
	if cr != 0.6 || cw != 4.5 {
		t.Errorf("explicit rates not respected: read=%v write=%v", cr, cw)
	}
}

// #559 C: Go-constructed rates (no JSON presence marker) with nonzero cache
// values keep working for Merge()-style programmatic tables.
func TestIssue559C_ProgrammaticNonzeroRates(t *testing.T) {
	rate := ModelRate{InputPerM: 3.0, OutputPerM: 15.0, CacheReadPerM: 0.6, CacheWritePerM: 4.5, Type: PricingPerToken}
	cr, cw := effectiveCacheRates(rate)
	if !nearlyEqual(cr, 0.6) || !nearlyEqual(cw, 4.5) {
		t.Errorf("programmatic rates not respected: read=%v write=%v", cr, cw)
	}
}

func nearlyEqual(a, b float64) bool {
	return a-b < 1e-9 && b-a < 1e-9
}
