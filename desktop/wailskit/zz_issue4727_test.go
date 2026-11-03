package wailskit

import "testing"

func TestDesktopResolveRateMirrorsTUIChain(t *testing.T) {

	if r := desktopResolveRate("openai", "https://api.example.com/cn-coding-openi/v1", "gpt-x"); r.Type != "subscription" || r.Plan != "Coding Plan" {
		t.Fatalf("coding-plan endpoint: got type=%q plan=%q, want subscription/Coding Plan", r.Type, r.Plan)
	}

	if r := desktopResolveRate("kimi", "https://api.moonshot.cn/v1", "kimi-k2"); r.Type != "subscription" || r.Plan == "" {
		t.Fatalf("subscription vendor: got type=%q plan=%q, want subscription with non-empty plan", r.Type, r.Plan)
	}

	if r := desktopResolveRate("unknown-vendor", "https://example.com/v1", "mystery-model"); r.Type != "" {
		t.Fatalf("unknown vendor: got type=%q, want empty (metered/unknown)", r.Type)
	}

	if r := desktopResolveRate("github-copilot", "https://api.example.com/v1", "gpt-5-codex"); r.Type == "" {
		t.Fatalf("pricing-table vendor github-copilot should resolve non-empty, got %+v", r)
	}
}

func TestUsagePayloadCarriesPlanKeys(t *testing.T) {
	b := &ChatBridge{}
	p := b.currentUsagePayload()
	if _, ok := p["pricingType"]; !ok {
		t.Fatal("payload missing pricingType key")
	}
	if _, ok := p["planLabel"]; !ok {
		t.Fatal("payload missing planLabel key")
	}
}
