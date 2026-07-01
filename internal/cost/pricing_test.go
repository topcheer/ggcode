package cost

import "testing"

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
				t.Errorf("%s/%s has hardcoded per-token price (in=%.2f, out=%.2f) — should only have type info",
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
		{"zai", ""},       // mixed — has both coding and standard endpoints
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
