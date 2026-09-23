package cost

import (
	"encoding/json"
	"testing"
)

// sa-108 coverage net: table-driven tests for the function-level 0% set
// (UnmarshalJSON, DisplayInputTokens, FormatCost) plus the uncovered Get()
// boundary branches (#559 Bug A/B semantics preserved as invariants).

// --- ModelRate.UnmarshalJSON (#559 Bug C presence markers) ---

func TestModelRate_UnmarshalJSON_PresenceMarkers_sa108(t *testing.T) {
	tests := []struct {
		name         string
		json         string
		wantRate     ModelRate
		wantReadSet  bool
		wantWriteSet bool
	}{
		{
			name:         "explicit cache_read zero (Bug C: free reads must be preserved)",
			json:         `{"type":"per_token","input_per_m":18,"cache_read_per_m":0}`,
			wantRate:     ModelRate{InputPerM: 18, Type: PricingPerToken},
			wantReadSet:  true,
			wantWriteSet: false,
		},
		{
			name:         "both cache fields explicit",
			json:         `{"cache_read_per_m":1.8,"cache_write_per_m":18}`,
			wantRate:     ModelRate{CacheReadPerM: 1.8, CacheWritePerM: 18},
			wantReadSet:  true,
			wantWriteSet: true,
		},
		{
			name:         "absent cache fields (no markers)",
			json:         `{"type":"subscription","plan":"GitHub Copilot"}`,
			wantRate:     ModelRate{Type: PricingSubscription, Plan: "GitHub Copilot"},
			wantReadSet:  false,
			wantWriteSet: false,
		},
		{
			name:         "empty object",
			json:         `{}`,
			wantRate:     ModelRate{},
			wantReadSet:  false,
			wantWriteSet: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r ModelRate
			if err := json.Unmarshal([]byte(tt.json), &r); err != nil {
				t.Fatalf("UnmarshalJSON(%s) error: %v", tt.json, err)
			}
			if r.Type != tt.wantRate.Type || r.Plan != tt.wantRate.Plan ||
				r.InputPerM != tt.wantRate.InputPerM ||
				r.CacheReadPerM != tt.wantRate.CacheReadPerM ||
				r.CacheWritePerM != tt.wantRate.CacheWritePerM {
				t.Errorf("rate = %+v, want %+v", r, tt.wantRate)
			}
			if r.CacheReadSet != tt.wantReadSet {
				t.Errorf("CacheReadSet = %v, want %v", r.CacheReadSet, tt.wantReadSet)
			}
			if r.CacheWriteSet != tt.wantWriteSet {
				t.Errorf("CacheWriteSet = %v, want %v", r.CacheWriteSet, tt.wantWriteSet)
			}
		})
	}
}

func TestModelRate_UnmarshalJSON_InvalidJSON_sa108(t *testing.T) {
	var r ModelRate
	if err := json.Unmarshal([]byte(`{invalid`), &r); err == nil {
		t.Error("expected error for invalid JSON")
	}
	// Type mismatch (string where float expected) must also error.
	if err := json.Unmarshal([]byte(`{"input_per_m":"x"}`), &r); err == nil {
		t.Error("expected error for type-mismatched JSON")
	}
}

// --- TokenUsage.DisplayInputTokens (#1529 subset semantics) ---

func TestTokenUsage_DisplayInputTokens_sa108(t *testing.T) {
	tests := []struct {
		name string
		u    TokenUsage
		want int
	}{
		{"no cache read returns input as-is", TokenUsage{InputTokens: 100}, 100},
		{"zero cache read", TokenUsage{InputTokens: 100, CacheRead: 0}, 100},
		{"cache read without total (legacy: input already uncached)", TokenUsage{InputTokens: 90, CacheRead: 10}, 90},
		{
			// OpenAI-compat subset semantics: PromptTokensTotal is the whole,
			// CacheRead the subset -> display uncached remainder.
			"subset normalization",
			TokenUsage{InputTokens: 100, CacheRead: 40, PromptTokensTotal: 100},
			60,
		},
		{
			// Remainder larger than InputTokens -> clamp to InputTokens.
			"normalized clamp to input",
			TokenUsage{InputTokens: 50, CacheRead: 10, PromptTokensTotal: 100},
			50,
		},
		{
			// Negative remainder (inconsistent totals) -> fall through to InputTokens.
			"negative remainder falls back",
			TokenUsage{InputTokens: 30, CacheRead: 70, PromptTokensTotal: 100},
			30,
		},
		{
			// Total present but zero -> ignored, returns InputTokens.
			"zero total ignored",
			TokenUsage{InputTokens: 80, CacheRead: 20, PromptTokensTotal: 0},
			80,
		},
		{
			// Remainder exactly equals InputTokens.
			"normalized equals input",
			TokenUsage{InputTokens: 60, CacheRead: 40, PromptTokensTotal: 100},
			60,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.u.DisplayInputTokens(); got != tt.want {
				t.Errorf("DisplayInputTokens(%+v) = %d, want %d", tt.u, got, tt.want)
			}
		})
	}
}

// --- FormatCost ---

func TestFormatCost_sa108(t *testing.T) {
	tests := []struct {
		usd  float64
		want string
	}{
		{0, "$0.0000"},
		{0.009, "$0.0090"},
		{0.0099, "$0.0099"},
		{0.01, "$0.01"},
		{1.5, "$1.50"},
		{123.456, "$123.46"},
		{-0.005, "-$0.0050"},
		{-12.345, "-$12.35"},
		{1000000, "$1000000.00"},
	}
	for _, tt := range tests {
		if got := FormatCost(tt.usd); got != tt.want {
			t.Errorf("FormatCost(%v) = %q, want %q", tt.usd, got, tt.want)
		}
	}
}

// --- PricingTable.Get boundary branches (#559 Bug A/B) ---

func TestPricingTable_Get_PrefixForwardOnly_sa108(t *testing.T) {
	pt := PricingTable{
		"zhipu": {
			"glm-4.5-air": {Type: PricingFree, Plan: "Zhipu Free Tier"},
			"glm-4.5":     {Type: PricingPerToken, InputPerM: 11},
		},
	}
	// Bug A forward-only invariant: a SHORTER query must not hit a LONGER key.
	rate, ok := pt.Get("zhipu", "glm-4.5")
	if !ok {
		t.Fatal("exact key glm-4.5 must match")
	}
	if rate.Type != PricingPerToken {
		t.Errorf("glm-4.5 = %s, want per_token (must not fall through to glm-4.5-air)", rate.Type)
	}
	// Longer query still prefixes the shorter key (forward direction OK).
	rate, ok = pt.Get("zhipu", "glm-4.5-air-2025")
	if !ok || rate.Type != PricingFree {
		t.Errorf("glm-4.5-air-2025 should prefix-match free air rate, got %+v ok=%v", rate, ok)
	}
}

func TestPricingTable_Get_SuffixBoundary_sa108(t *testing.T) {
	pt := PricingTable{
		"github-copilot": {
			"o3":      {Type: PricingSubscription, Plan: "GitHub Copilot"},
			"o3-mini": {Type: PricingSubscription, Plan: "GitHub Copilot"},
		},
	}
	// Bug B: "my-proxy-o3" must NOT hit "o3" ("-" is not a boundary).
	if _, ok := pt.Get("github-copilot", "my-proxy-o3"); ok {
		t.Error(`"my-proxy-o3" must not suffix-match "o3" (hyphen is not a boundary)`)
	}
	// Path separator IS a boundary: "anthropic/o3" hits "o3".
	rate, ok := pt.Get("github-copilot", "anthropic/o3")
	if !ok {
		t.Fatal(`"anthropic/o3" should suffix-match "o3" (path separator boundary)`)
	}
	if rate.Type != PricingSubscription {
		t.Errorf("anthropic/o3 rate type = %s, want subscription", rate.Type)
	}
	// Longest suffix wins: "anthropic/o3-mini" prefers "o3-mini" over "o3".
	rate, ok = pt.Get("github-copilot", "anthropic/o3-mini")
	if !ok {
		t.Fatal(`"anthropic/o3-mini" should suffix-match`)
	}
	if rate.Plan != "GitHub Copilot" {
		t.Errorf("unexpected plan %q", rate.Plan)
	}
	// Exact name qualifies as its own suffix (empty remaining).
	if _, ok := pt.Get("github-copilot", "o3"); !ok {
		t.Error(`exact "o3" must match`)
	}
	// Suffix with non-boundary remainder of len>0 is rejected.
	if _, ok := pt.Get("github-copilot", "xxo3"); ok {
		t.Error(`"xxo3" must not suffix-match "o3"`)
	}
}

func TestPricingTable_Get_ProviderLowercaseFallback_sa108(t *testing.T) {
	pt := PricingTable{
		"anthropic": {"claude-sonnet-4-6": {Type: PricingPerToken, InputPerM: 3}},
	}
	rate, ok := pt.Get("Anthropic", "claude-sonnet-4-6")
	if !ok || rate.InputPerM != 3 {
		t.Errorf("mixed-case provider should hit lowercase fallback, got %+v ok=%v", rate, ok)
	}
	if _, ok := pt.Get("AnthropicX", "claude-sonnet-4-6"); ok {
		t.Error("unknown provider must not match")
	}
}

func TestPricingTable_Merge_IsolatedCopy_sa108(t *testing.T) {
	base := PricingTable{"zai": {"glm-4.6": {Type: PricingPerToken}}}
	overlay := PricingTable{"zai": {"glm-4.6": {Type: PricingSubscription, Plan: "GLM Coding Plan"}}}
	merged := base.Merge(overlay)
	// Overlay wins in merged copy...
	if r, ok := merged.Get("zai", "glm-4.6"); !ok || r.Type != PricingSubscription {
		t.Errorf("overlay should win, got %+v ok=%v", r, ok)
	}
	// ...but neither input table is mutated.
	if r, _ := base.Get("zai", "glm-4.6"); r.Type != PricingPerToken {
		t.Errorf("base table must not be mutated, got %s", r.Type)
	}
	if r, _ := overlay.Get("zai", "glm-4.6"); r.Plan != "GLM Coding Plan" {
		t.Errorf("overlay table must not be mutated, got %q", r.Plan)
	}
	// Merge onto an empty table yields the overlay providers.
	empty := PricingTable{}
	if _, ok := empty.Merge(overlay).Get("zai", "glm-4.6"); !ok {
		t.Error("merge onto empty table must carry overlay entries")
	}
}

// DefaultPricingTable must return stable cached content (read-only hot path,
// 316MB/frame regression guard relies on sync.OnceValue).
func TestDefaultPricingTable_CachedInstance_sa108(t *testing.T) {
	a := DefaultPricingTable()
	b := DefaultPricingTable()
	if len(a) == 0 {
		t.Fatal("default table must not be empty")
	}
	if len(a) != len(b) {
		t.Error("DefaultPricingTable() content must be stable across calls")
	}
	for vendor, models := range a {
		bm, ok := b[vendor]
		if !ok || len(bm) != len(models) {
			t.Errorf("vendor %q missing/different between calls", vendor)
		}
	}
}
