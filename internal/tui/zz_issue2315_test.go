package tui

// #2315: both live cost formulas billed cached tokens twice on
// subset-semantics vendors (InputTokens already contains CacheRead) -
// the status bar and /cost disagreed with the sidebar's normalized
// Input display. The formulas now use DisplayInputTokens.

import (
	"fmt"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

func TestIssue2315SubsetBillingNoDoubleCharge(t *testing.T) {
	// OpenAI-compat shape (openai.go L784 same-value double fill):
	// InputTokens is the SUPERSET and CacheRead the subset.
	u := provider.TokenUsage{
		InputTokens:       100_000, // includes the 88k cached
		PromptTokensTotal: 100_000, // openai fills both with the superset
		CacheRead:         88_000,
		OutputTokens:      1_000,
	}
	rate := rateFor2315(3.0, 1.5, 0.30, 0.375)
	old := float64(u.InputTokens)*rate.InputPerM/1e6 +
		float64(u.CacheRead)*rate.CacheReadPerM/1e6
	got := float64(u.DisplayInputTokens())*rate.InputPerM/1e6 +
		float64(u.CacheRead)*rate.CacheReadPerM/1e6
	if got >= old {
		t.Fatalf("normalized billing must be cheaper: old=%v new=%v", old, got)
	}
	// Overcharge eliminated: the 88k cached tokens stop being billed at
	// full input price = 88_000 * $3.0/M = $0.264/turn (the issue's ~$0.24
	// used the price-difference framing; identical order of magnitude).
	saved := old - got
	if saved < 0.26 || saved > 0.27 {
		t.Fatalf("expected ~$0.264 overcharge eliminated, saved=%v", saved)
	}
}

type rate2315 struct{ InputPerM, OutputPerM, CacheReadPerM, CacheWritePerM float64 }

func rateFor2315(in, out, cr, cw float64) rate2315 { return rate2315{in, out, cr, cw} }

// #3434: the SIDEBAR cost row (sidebarEstimatedCost) and the status-bar
// fallback (estimateSessionCost with no UsageHistory) were the two sister
// paths #2315 missed - both billed the superset InputTokens at full input
// price AND CacheRead at the cache price (double charge, up to ~2x the
// input component at high cache-hit ratios). Both now use
// DisplayInputTokens. This probe pins the sidebar path end-to-end against
// the same resolveRate the production code uses.
func TestIssue2315SidebarCostNormalized(t *testing.T) {
	u := provider.TokenUsage{
		InputTokens:       100_000, // openai-compat superset shape
		PromptTokensTotal: 100_000,
		CacheRead:         88_000,
		OutputTokens:      1_000,
	}
	m := Model{session: &session.Session{
		Vendor:   "anthropic",
		Endpoint: "anthropic-api",
		Model:    "claude-sonnet-4-5",
	}}
	rate := resolveRate(m.session.Vendor, m.session.Endpoint, m.session.Model)
	if !rate.IsKnown() || !rate.IsMetered() {
		t.Skip("pricing table drifted - no metered anthropic rate to pin against")
	}
	got := m.sidebarEstimatedCost(u)
	want := fmt.Sprintf("$%.4f",
		float64(u.DisplayInputTokens())*rate.InputPerM/1e6+
			float64(u.OutputTokens)*rate.OutputPerM/1e6+
			float64(u.CacheRead)*rate.CacheReadPerM/1e6+
			float64(u.CacheWrite)*rate.CacheWritePerM/1e6)
	if got != want {
		t.Fatalf("sidebar cost must use the normalized formula: got %s want %s", got, want)
	}
	// And it must be strictly cheaper than the raw double-charge shape.
	raw := fmt.Sprintf("$%.4f",
		float64(u.InputTokens)*rate.InputPerM/1e6+
			float64(u.OutputTokens)*rate.OutputPerM/1e6+
			float64(u.CacheRead)*rate.CacheReadPerM/1e6)
	if got == raw && u.CacheRead > 0 && rate.InputPerM > rate.CacheReadPerM {
		t.Fatalf("superset usage with cache-read must not equal raw double-charge: %s", got)
	}
}
