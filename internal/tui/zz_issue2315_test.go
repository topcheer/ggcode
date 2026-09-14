package tui

// #2315: both live cost formulas billed cached tokens twice on
// subset-semantics vendors (InputTokens already contains CacheRead) -
// the status bar and /cost disagreed with the sidebar's normalized
// Input display. The formulas now use DisplayInputTokens.

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
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
