package cost

import (
	"encoding/json"
	"testing"
)

func TestModelRate_UnmarshalJSON(t *testing.T) {
	t.Run("explicit zero cache rates are marked as set (#559 Bug C)", func(t *testing.T) {
		var r ModelRate
		if err := json.Unmarshal([]byte(`{"input_per_m":3,"cache_read_per_m":0,"cache_write_per_m":0}`), &r); err != nil {
			t.Fatal(err)
		}
		if !r.CacheReadSet || !r.CacheWriteSet {
			t.Errorf("expected CacheReadSet/CacheWriteSet true, got %v/%v", r.CacheReadSet, r.CacheWriteSet)
		}
		if r.CacheReadPerM != 0 || r.CacheWritePerM != 0 {
			t.Errorf("expected zero cache rates, got %v/%v", r.CacheReadPerM, r.CacheWritePerM)
		}
	})
	t.Run("absent cache fields are not marked", func(t *testing.T) {
		var r ModelRate
		if err := json.Unmarshal([]byte(`{"input_per_m":3,"output_per_m":15}`), &r); err != nil {
			t.Fatal(err)
		}
		if r.CacheReadSet || r.CacheWriteSet {
			t.Errorf("expected CacheReadSet/CacheWriteSet false, got %v/%v", r.CacheReadSet, r.CacheWriteSet)
		}
	})
	t.Run("present non-zero cache fields are marked", func(t *testing.T) {
		var r ModelRate
		if err := json.Unmarshal([]byte(`{"cache_read_per_m":0.5}`), &r); err != nil {
			t.Fatal(err)
		}
		if !r.CacheReadSet || r.CacheReadPerM != 0.5 {
			t.Errorf("expected CacheReadSet with 0.5, got %v/%v", r.CacheReadSet, r.CacheReadPerM)
		}
	})
	t.Run("invalid json returns error", func(t *testing.T) {
		var r ModelRate
		if err := json.Unmarshal([]byte(`{invalid`), &r); err == nil {
			t.Error("expected error for malformed JSON")
		}
	})
}

func TestPricingTable_Get_SuffixMatch(t *testing.T) {
	table := PricingTable{
		"test": {
			"o3":    {Type: PricingSubscription, Plan: "o3-plan"},
			"gpt-4": {Type: PricingPerToken, InputPerM: 1},
			"4":     {Type: PricingFree, Plan: "digit"},
		},
	}
	t.Run("path-suffixed query hits short key", func(t *testing.T) {
		rate, ok := table.Get("test", "anthropic/o3")
		if !ok {
			t.Fatal("expected suffix match for anthropic/o3")
		}
		if rate.Plan != "o3-plan" {
			t.Errorf("expected o3-plan, got %q", rate.Plan)
		}
	})
	t.Run("longest suffix wins with boundary respected", func(t *testing.T) {
		// "4" is a longer-relative suffix candidate but "org/gpt-" lacks a "/"
		// boundary, so it must be skipped; "gpt-4" wins as the longest valid one.
		rate, ok := table.Get("test", "org/gpt-4")
		if !ok {
			t.Fatal("expected suffix match for org/gpt-4")
		}
		if rate.Type != PricingPerToken || rate.InputPerM != 1 {
			t.Errorf("expected gpt-4 rate, got %+v", rate)
		}
	})
	t.Run("hyphen boundary rejected (#559 Bug B)", func(t *testing.T) {
		// "my-proxy-o3" ends with "-o3", not "/o3" - must NOT hit the o3 rate.
		if _, ok := table.Get("test", "my-proxy-o3"); ok {
			t.Error("expected no match for my-proxy-o3 (hyphen is not a boundary)")
		}
	})
}

func TestPricingTable_Get_PrefixForwardOnly(t *testing.T) {
	table := PricingTable{
		"p": {
			"glm-4.5-air": {Type: PricingFree, Plan: "air"},
			"glm-4.5":     {Type: PricingPerToken, InputPerM: 2},
		},
	}
	t.Run("query shorter than key must not match (#559 Bug A)", func(t *testing.T) {
		// Reverse-prefix matching would make "glm-4" hit "glm-4.5-air" (free)
		// and understate costs; forward-only means no match.
		if _, ok := table.Get("p", "glm-4"); ok {
			t.Error("expected no match: query being a prefix of a key must not match")
		}
	})
	t.Run("longer query matches shortest containing prefix", func(t *testing.T) {
		rate, ok := table.Get("p", "glm-4.5-x")
		if !ok {
			t.Fatal("expected prefix match for glm-4.5-x")
		}
		if rate.Type != PricingPerToken || rate.InputPerM != 2 {
			t.Errorf("expected glm-4.5 (paid) rate, got %+v", rate)
		}
	})
}

func TestPricingTable_Get_ProviderCaseFallback(t *testing.T) {
	// Uppercase provider names fall back to the lowercased table key.
	rate, ok := DefaultPricingTable().Get("ZHIPU", "glm-4.5-air")
	if !ok {
		t.Fatal("expected provider case-insensitive fallback to find zhipu/glm-4.5-air")
	}
	if rate.Type != PricingFree {
		t.Errorf("expected free tier, got %s", rate.Type)
	}
}
