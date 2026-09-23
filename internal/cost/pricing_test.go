package cost

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestPricingTable_ExactMatch(t *testing.T) {
	pt := DefaultPricingTable()
	rate, ok := pt.Get("github-copilot", "gpt-4o")
	if !ok {
		t.Fatal("expected exact match for github-copilot/gpt-4o")
	}
	if rate.Type != PricingSubscription {
		t.Errorf("expected subscription type, got %s", rate.Type)
	}
}

func TestPricingTable_CaseInsensitive(t *testing.T) {
	pt := DefaultPricingTable()
	rate, ok := pt.Get("GitHub-Copilot", "GPT-4o")
	if !ok {
		t.Fatal("expected case-insensitive match")
	}
	if rate.Plan != "GitHub Copilot" {
		t.Errorf("expected plan 'GitHub Copilot', got %q", rate.Plan)
	}
}

func TestPricingTable_PrefixMatch(t *testing.T) {
	pt := DefaultPricingTable()
	// "gpt-4o-2024-08-06" should match "gpt-4o" prefix
	rate, ok := pt.Get("github-copilot", "gpt-4o-2024-08-06")
	if !ok {
		t.Fatal("expected prefix match")
	}
	if rate.Type != PricingSubscription {
		t.Errorf("expected subscription, got %s", rate.Type)
	}
}

func TestPricingTable_NotFound(t *testing.T) {
	pt := DefaultPricingTable()
	_, ok := pt.Get("unknown", "unknown-model")
	if ok {
		t.Error("expected not found for unknown provider/model")
	}
}

func TestPricingTable_NoFakePerTokenPrices(t *testing.T) {
	// Verify that NO vendor has hardcoded per-token prices.
	// Per-token pricing should only come from user Merge().
	pt := DefaultPricingTable()
	for vendor, models := range pt {
		for model, rate := range models {
			if rate.InputPerM > 0 || rate.OutputPerM > 0 {
				t.Errorf("%s/%s has hardcoded per-token price (in=%.2f, out=%.2f) - should only have type info",
					vendor, model, rate.InputPerM, rate.OutputPerM)
			}
		}
	}
}

func TestPricingTable_Merge(t *testing.T) {
	base := DefaultPricingTable()
	custom := PricingTable{
		"anthropic": {
			"claude-sonnet-4-6": {Type: PricingPerToken, InputPerM: 3.0, OutputPerM: 15.0},
		},
	}
	merged := base.Merge(custom)
	// Copilot entries should survive merge
	rate, ok := merged.Get("github-copilot", "gpt-4o")
	if !ok {
		t.Error("base entries should survive merge")
	}
	if rate.Type != PricingSubscription {
		t.Error("copilot should still be subscription after merge")
	}
	// Custom entries should be added
	rate, ok = merged.Get("anthropic", "claude-sonnet-4-6")
	if !ok {
		t.Error("custom entries should be added")
	}
	if !rate.IsMetered() {
		t.Error("custom anthropic entry should be metered")
	}
}

func TestIsCodingPlanEndpoint(t *testing.T) {
	tests := []struct {
		endpoint string
		want     bool
	}{
		{"cn-coding-openai", true},
		{"global-coding-anthropic", true},
		{"cn-coding-plan", true},
		{"coding-lite", true},
		{"token-plan-cn", true},
		{"cn-api-openai", false},
		{"global-api-openai", false},
		{"", false},
		{"standard", false},
	}
	for _, tt := range tests {
		t.Run(tt.endpoint, func(t *testing.T) {
			if got := IsCodingPlanEndpoint(tt.endpoint); got != tt.want {
				t.Errorf("IsCodingPlanEndpoint(%q) = %v, want %v", tt.endpoint, got, tt.want)
			}
		})
	}
}

func TestIsSubscriptionVendor(t *testing.T) {
	tests := []struct {
		vendor   string
		wantPlan string
	}{
		{"kimi", "Kimi Coding Plan"},
		{"ark", "Volcengine Ark Coding Plan"},
		{"aliyun", "Aliyun Bailian Coding Plan"},
		{"minimax", "MiniMax Token Plan"},
		{"xiaomi-mimo", "Xiaomi MiMo Token Plan"},
		{"github-copilot", "GitHub Copilot"},
		{"zai", ""},       // mixed - has both coding and standard endpoints
		{"anthropic", ""}, // per-token only
		{"", ""},          // empty
	}
	for _, tt := range tests {
		t.Run(tt.vendor, func(t *testing.T) {
			got := IsSubscriptionVendor(tt.vendor)
			if got != tt.wantPlan {
				t.Errorf("IsSubscriptionVendor(%q) = %q, want %q", tt.vendor, got, tt.wantPlan)
			}
		})
	}
}

func TestModelRate_IsMetered(t *testing.T) {
	tests := []struct {
		name string
		rate ModelRate
		want bool
	}{
		{"unknown (default)", ModelRate{}, false},
		{"per_token", ModelRate{Type: PricingPerToken, InputPerM: 3.0}, true},
		{"subscription", ModelRate{Type: PricingSubscription}, false},
		{"free", ModelRate{Type: PricingFree}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rate.IsMetered(); got != tt.want {
				t.Errorf("IsMetered() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestModelRate_IsKnown(t *testing.T) {
	tests := []struct {
		name string
		rate ModelRate
		want bool
	}{
		{"empty", ModelRate{}, false},
		{"subscription", ModelRate{Type: PricingSubscription}, true},
		{"free", ModelRate{Type: PricingFree}, true},
		{"per_token", ModelRate{Type: PricingPerToken, InputPerM: 3.0}, true},
		{"unknown type", ModelRate{Type: PricingUnknown}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rate.IsKnown(); got != tt.want {
				t.Errorf("IsKnown() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestPricingTable_BugAForwardOnlyPrefix pins the #559 (Bug A) regression fix:
// a query that is a PREFIX of a table key must not match the longer key.
// "glm-4.5" (paid) previously hit "glm-4.5-air" (free) and $11 showed as $0.
func TestPricingTable_BugAForwardOnlyPrefix(t *testing.T) {
	pt := DefaultPricingTable()
	if _, ok := pt.Get("zhipu", "glm-4.5"); ok {
		t.Error(`Get("zhipu", "glm-4.5") matched "glm-4.5-air" - forward-only prefix rule broken`)
	}
	// The versioned form still resolves to the free model.
	rate, ok := pt.Get("zhipu", "glm-4.5-air-2025-07")
	if !ok || rate.Type != PricingFree {
		t.Errorf(`Get("zhipu", "glm-4.5-air-2025-07") = (%+v, %v), want free tier`, rate, ok)
	}
}

// TestPricingTable_BugBSuffixBoundary pins the #559 (Bug B) regression fix:
// suffix matches require a hard "/" boundary so "my-proxy-o3" no longer hits
// the "o3" subscription rate ($180 shown as included).
func TestPricingTable_BugBSuffixBoundary(t *testing.T) {
	pt := PricingTable{
		"prov": {
			"o3": {Type: PricingSubscription, Plan: "o3"},
		},
	}
	if _, ok := pt.Get("prov", "my-proxy-o3"); ok {
		t.Error(`Get("prov", "my-proxy-o3") matched "o3" - suffix boundary rule broken`)
	}
	// A path-qualified name still resolves via the "/" boundary.
	rate, ok := pt.Get("prov", "gateway/ns/o3")
	if !ok || rate.Plan != "o3" {
		t.Errorf(`Get("prov", "gateway/ns/o3") = (%+v, %v), want "o3" rate`, rate, ok)
	}
	// Exact name qualifies even without any boundary prefix.
	if rate, ok := pt.Get("prov", "o3"); !ok || rate.Plan != "o3" {
		t.Errorf(`Get("prov", "o3") = (%+v, %v), want exact match`, rate, ok)
	}
}

func TestPricingTable_SuffixLongestWins(t *testing.T) {
	pt := PricingTable{
		"prov": {
			"o3":   {Type: PricingFree, Plan: "generic-o3"},
			"x/o3": {Type: PricingSubscription, Plan: "x-o3"},
		},
	}
	rate, ok := pt.Get("prov", "gateway/x/o3")
	if !ok || rate.Plan != "x-o3" {
		t.Errorf("longest suffix key should win, got (%+v, %v)", rate, ok)
	}
	rate, ok = pt.Get("prov", "gateway/other/o3")
	if !ok || rate.Plan != "generic-o3" {
		t.Errorf(`"gateway/other/o3" should fall back to "o3", got (%+v, %v)`, rate, ok)
	}
}

func TestPricingTable_PrefixLongestWins(t *testing.T) {
	pt := PricingTable{
		"prov": {
			"gpt-4":  {Type: PricingSubscription, Plan: "gpt-4"},
			"gpt-4o": {Type: PricingFree, Plan: "gpt-4o"},
		},
	}
	rate, ok := pt.Get("prov", "gpt-4o-2024-11-20")
	if !ok || rate.Plan != "gpt-4o" {
		t.Errorf("longest prefix key should win, got (%+v, %v)", rate, ok)
	}
	rate, ok = pt.Get("prov", "gpt-4-0613")
	if !ok || rate.Plan != "gpt-4" {
		t.Errorf(`"gpt-4-0613" should match "gpt-4", got (%+v, %v)`, rate, ok)
	}
}

func TestPricingTable_CaseInsensitiveExactBeatsPrefix(t *testing.T) {
	pt := PricingTable{
		"prov": {
			"GPT-4o":   {Type: PricingSubscription, Plan: "exact"},
			"gpt-4o-x": {Type: PricingFree, Plan: "prefix"},
		},
	}
	rate, ok := pt.Get("prov", "gpt-4o")
	if !ok || rate.Plan != "exact" {
		t.Errorf("case-insensitive exact match should win over prefix, got (%+v, %v)", rate, ok)
	}
}

func TestPricingTable_EmptyAndNil(t *testing.T) {
	var nilPT PricingTable
	if _, ok := nilPT.Get("zai", "glm-4.6"); ok {
		t.Error("nil table should not match")
	}
	empty := PricingTable{}
	if _, ok := empty.Get("zai", "glm-4.6"); ok {
		t.Error("empty table should not match")
	}
	// Known provider, unknown model: no fuzzy path can rescue it.
	pt := PricingTable{"prov": {"a": {Type: PricingFree}}}
	if _, ok := pt.Get("prov", "zzz"); ok {
		t.Error("known provider with unknown model should not match")
	}
	// Unknown provider must not fall through to another provider's models.
	if _, ok := pt.Get("other", "a"); ok {
		t.Error("unknown provider should not match")
	}
}

func TestPricingTable_MergeImmutability(t *testing.T) {
	base := PricingTable{"prov": {"m": {Type: PricingSubscription, Plan: "base"}}}
	custom := PricingTable{
		"prov": {"m": {Type: PricingPerToken, InputPerM: 3.0}},
		"new":  {"n": {Type: PricingFree}},
	}
	merged := base.Merge(custom)

	if rate, _ := base.Get("prov", "m"); rate.Type != PricingSubscription {
		t.Errorf("base mutated by Merge: base rate is now %s", rate.Type)
	}
	if _, ok := base.Get("new", "n"); ok {
		t.Error("base gained entries from Merge")
	}
	rate, ok := merged.Get("prov", "m")
	if !ok || rate.Type != PricingPerToken || rate.InputPerM != 3.0 {
		t.Errorf("merged override wrong: %+v ok=%v", rate, ok)
	}
	if _, ok := merged.Get("new", "n"); !ok {
		t.Error("merged should contain new provider entries")
	}
	// Mutating the merged copy must not leak into base.
	merged["prov"]["m"] = ModelRate{Type: PricingFree}
	if rate, _ := base.Get("prov", "m"); rate.Type != PricingSubscription {
		t.Error("mutating merged table leaked into base")
	}
	// Merge(nil) is a copy of base.
	if got := base.Merge(nil); len(got) != len(base) {
		t.Errorf("Merge(nil) changed table size: got %d, want %d", len(got), len(base))
	}
}

func TestModelRate_UnmarshalJSON_CachePresenceMarkers(t *testing.T) {
	// #559 (Bug C): an explicitly configured cache_read_per_m: 0 (free cache
	// reads) must be distinguishable from an absent field. Without the
	// presence marker it fell back to the 0.10x-input heuristic ($18 → $123).
	tests := []struct {
		name         string
		raw          string
		wantReadSet  bool
		wantWriteSet bool
		wantRead     float64
		wantWrite    float64
	}{
		{"absent cache fields", `{"input_per_m":3}`, false, false, 0, 0},
		{"explicit zero read", `{"cache_read_per_m":0}`, true, false, 0, 0},
		{"explicit zero write", `{"cache_write_per_m":0}`, false, true, 0, 0},
		{"both explicit", `{"cache_read_per_m":0.1,"cache_write_per_m":1.25}`, true, true, 0.1, 1.25},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r ModelRate
			if err := json.Unmarshal([]byte(tt.raw), &r); err != nil {
				t.Fatalf("UnmarshalJSON(%s) error: %v", tt.raw, err)
			}
			if r.CacheReadSet != tt.wantReadSet {
				t.Errorf("CacheReadSet = %v, want %v", r.CacheReadSet, tt.wantReadSet)
			}
			if r.CacheWriteSet != tt.wantWriteSet {
				t.Errorf("CacheWriteSet = %v, want %v", r.CacheWriteSet, tt.wantWriteSet)
			}
			if r.CacheReadPerM != tt.wantRead {
				t.Errorf("CacheReadPerM = %v, want %v", r.CacheReadPerM, tt.wantRead)
			}
			if r.CacheWritePerM != tt.wantWrite {
				t.Errorf("CacheWritePerM = %v, want %v", r.CacheWritePerM, tt.wantWrite)
			}
		})
	}
}

func TestModelRate_UnmarshalJSON_Malformed(t *testing.T) {
	var r ModelRate
	if err := json.Unmarshal([]byte(`{invalid`), &r); err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestModelRate_UnmarshalJSON_PreservesOtherFields(t *testing.T) {
	var r ModelRate
	raw := `{"input_per_m":3.0,"output_per_m":15.0,"type":"per_token","plan":"Custom"}`
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatalf("UnmarshalJSON error: %v", err)
	}
	if r.InputPerM != 3.0 || r.OutputPerM != 15.0 || r.Type != PricingPerToken || r.Plan != "Custom" {
		t.Errorf("fields lost in unmarshal: %+v", r)
	}
}

func TestDefaultPricingTable_Cached(t *testing.T) {
	// DefaultPricingTable() sits on hot paths (estimateSessionCost walks every
	// UsageHistory entry per View frame); it must return the cached instance.
	a := DefaultPricingTable()
	b := DefaultPricingTable()
	if reflect.ValueOf(a).Pointer() != reflect.ValueOf(b).Pointer() {
		t.Error("DefaultPricingTable should return the cached instance, not a fresh table")
	}
}

func TestIsCodingPlanEndpoint_CaseAndUnderscore(t *testing.T) {
	tests := []struct {
		endpoint string
		want     bool
	}{
		{"CN-CODING-OPENAI", true},  // uppercase
		{"Coding-Lite", true},       // mixed case
		{"token_plan", true},        // underscore variant
		{"TOKEN-PLAN-CN", true},     // uppercase hyphen variant
		{"encoding-endpoint", true}, // substring matching is case-wide, not token-based
		{"   ", false},              // whitespace only: non-empty, but no keyword
	}
	for _, tt := range tests {
		t.Run(tt.endpoint, func(t *testing.T) {
			if got := IsCodingPlanEndpoint(tt.endpoint); got != tt.want {
				t.Errorf("IsCodingPlanEndpoint(%q) = %v, want %v", tt.endpoint, got, tt.want)
			}
		})
	}
}

func TestIsSubscriptionVendor_CaseInsensitive(t *testing.T) {
	if got := IsSubscriptionVendor("KIMI"); got != "Kimi Coding Plan" {
		t.Errorf("IsSubscriptionVendor(%q) = %q, want %q", "KIMI", got, "Kimi Coding Plan")
	}
	if got := IsSubscriptionVendor("GitHub-Copilot"); got != "GitHub Copilot" {
		t.Errorf("IsSubscriptionVendor(%q) = %q, want %q", "GitHub-Copilot", got, "GitHub Copilot")
	}
	if got := IsSubscriptionVendor("kimi "); got != "" {
		t.Errorf("IsSubscriptionVendor with trailing space = %q, want empty (no trimming)", got)
	}
}
