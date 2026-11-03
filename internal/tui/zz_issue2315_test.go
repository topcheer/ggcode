package tui

import (
	"fmt"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

func TestIssue2315SubsetBillingNoDoubleCharge(t *testing.T) {

	u := provider.TokenUsage{
		InputTokens:       100_000,
		PromptTokensTotal: 100_000,
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

	saved := old - got
	if saved < 0.26 || saved > 0.27 {
		t.Fatalf("expected ~$0.264 overcharge eliminated, saved=%v", saved)
	}
}

type rate2315 struct{ InputPerM, OutputPerM, CacheReadPerM, CacheWritePerM float64 }

func rateFor2315(in, out, cr, cw float64) rate2315 { return rate2315{in, out, cr, cw} }

func TestIssue2315SidebarCostNormalized(t *testing.T) {
	u := provider.TokenUsage{
		InputTokens:       100_000,
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

	raw := fmt.Sprintf("$%.4f",
		float64(u.InputTokens)*rate.InputPerM/1e6+
			float64(u.OutputTokens)*rate.OutputPerM/1e6+
			float64(u.CacheRead)*rate.CacheReadPerM/1e6)
	if got == raw && u.CacheRead > 0 && rate.InputPerM > rate.CacheReadPerM {
		t.Fatalf("superset usage with cache-read must not equal raw double-charge: %s", got)
	}
}
