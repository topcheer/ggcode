package wailskit

import "testing"

// #4727: desktopResolveRate must mirror the TUI's resolveRate billing-type
// chain (internal/tui commands_slash_info.go) so the desktop frontend can
// render "included in {plan}" for subscription sessions instead of a
// per-token estimated cost.
func TestDesktopResolveRateMirrorsTUIChain(t *testing.T) {
	// 2. Coding-plan endpoint -> subscription, plan label "Coding Plan".
	if r := desktopResolveRate("openai", "https://api.example.com/cn-coding-openi/v1", "gpt-x"); r.Type != "subscription" || r.Plan != "Coding Plan" {
		t.Fatalf("coding-plan endpoint: got type=%q plan=%q, want subscription/Coding Plan", r.Type, r.Plan)
	}
	// 3. Whole-vendor subscription (kimi & co) -> subscription with vendor plan.
	if r := desktopResolveRate("kimi", "https://api.moonshot.cn/v1", "kimi-k2"); r.Type != "subscription" || r.Plan == "" {
		t.Fatalf("subscription vendor: got type=%q plan=%q, want subscription with non-empty plan", r.Type, r.Plan)
	}
	// 4. Unknown metered vendor -> zero rate (frontend falls back to estimate).
	if r := desktopResolveRate("unknown-vendor", "https://example.com/v1", "mystery-model"); r.Type != "" {
		t.Fatalf("unknown vendor: got type=%q, want empty (metered/unknown)", r.Type)
	}
	// 1. Explicit pricing-table entry wins over the endpoint/vendor branches.
	if r := desktopResolveRate("github-copilot", "https://api.example.com/v1", "gpt-5-codex"); r.Type == "" {
		t.Fatalf("pricing-table vendor github-copilot should resolve non-empty, got %+v", r)
	}
}

// The usage payload must always carry the two #4727 keys (empty when no
// session), so the frontend contract is stable across session transitions.
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
