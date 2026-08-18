package cost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// zz683a: unknown model → HasPricing=false, display "(no pricing data)" not $0.0000.
func TestZZ683_UnknownModelShowsNoPricingData(t *testing.T) {
	tr := NewTracker("openai", "totally-unknown-model", PricingTable{})
	tr.Record(TokenUsage{InputTokens: 1000, OutputTokens: 2000})

	sc := tr.SessionCost()
	if sc.HasPricing {
		t.Fatal("HasPricing should be false for unknown model")
	}
	if sc.TotalCostUSD != 0 {
		t.Fatalf("TotalCostUSD should stay 0 when no pricing, got %v", sc.TotalCostUSD)
	}
	out := FormatSessionCost(sc, time.Now())
	if !strings.Contains(out, "(no pricing data)") {
		t.Errorf("expected '(no pricing data)', got: %q", out)
	}
	if strings.Contains(out, "$0.0000") {
		t.Errorf("false-precise $0.0000 still rendered: %q", out)
	}
}

// zz683a: known model still shows a real dollar figure.
func TestZZ683_KnownModelStillShowsDollars(t *testing.T) {
	tr := NewTracker("anthropic", "claude-3", PricingTable{
		"anthropic": {"claude-3": {InputPerM: 3, OutputPerM: 15, Type: PricingPerToken}},
	})
	tr.Record(TokenUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000})
	sc := tr.SessionCost()
	if !sc.HasPricing {
		t.Fatal("HasPricing should be true")
	}
	out := FormatSessionCost(sc, time.Now())
	if !strings.Contains(out, "$18.00") {
		t.Errorf("expected $18.00 in %q", out)
	}
}

// zz683a: .cost.json persisted without pricing must recalculate after Load
// once the pricing table knows the model.
func TestZZ683_LoadRecalculatesWhenPricingLaterKnown(t *testing.T) {
	dir := t.TempDir()

	// Persist a snapshot saved while pricing was unknown.
	sc := SessionCost{
		Provider: "openai", Model: "gpt-new",
		InputTokens: 1_000_000, OutputTokens: 1_000_000,
		HasPricing: false,
	}
	data, _ := json.Marshal(sc)
	if err := os.WriteFile(filepath.Join(dir, "sess1.cost.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	// New manager whose pricing table now knows the model.
	m := NewManager(PricingTable{
		"openai": {"gpt-new": {InputPerM: 1, OutputPerM: 2, Type: PricingPerToken}},
	}, dir)
	m.Load("sess1", "openai", "gpt-new")

	loaded, ok := m.SessionCost("sess1")
	if !ok {
		t.Fatal("session not loaded")
	}
	if !loaded.HasPricing {
		t.Fatal("HasPricing should flip true after Load recalculation")
	}
	if loaded.TotalCostUSD != 3.0 {
		t.Fatalf("expected recalc $3.00, got %v", loaded.TotalCostUSD)
	}
}

// zz683a: LoadAllFromDisk path also recalculates.
func TestZZ683_LoadAllRecalculates(t *testing.T) {
	dir := t.TempDir()
	sc := SessionCost{Provider: "p", Model: "m", InputTokens: 2_000_000}
	data, _ := json.Marshal(sc)
	if err := os.WriteFile(filepath.Join(dir, "s2.cost.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(PricingTable{
		"p": {"m": {InputPerM: 1, Type: PricingPerToken}},
	}, dir)
	if n := m.LoadAllFromDisk(); n != 1 {
		t.Fatalf("expected 1 loaded, got %d", n)
	}
	loaded, _ := m.SessionCost("s2")
	if loaded.TotalCostUSD != 2.0 {
		t.Fatalf("expected recalc $2.00, got %v", loaded.TotalCostUSD)
	}
}

// zz683b: FormatCost negative placement + net-loss line no double minus.
func TestZZ683_NetLossNoDoubleMinus(t *testing.T) {
	if got := FormatCost(-1.5); got != "-$1.50" {
		t.Errorf("FormatCost(-1.5) = %q, want -$1.50", got)
	}
	// PercentSaved negative (e.g. -633) previously rendered "--633%".
	a := CacheAnalysis{
		CacheReadTokens: 0, CacheWriteTokens: 10,
		NetSavingsUSD: -0.0123, PercentSaved: -633, HasPricing: true,
	}
	out := FormatCacheAnalysis(a)
	if strings.Contains(out, "--") {
		t.Errorf("double minus in %q", out)
	}
	if !strings.Contains(out, "(-633%)") {
		t.Errorf("expected (-633%%) in %q", out)
	}
	if !strings.Contains(out, "net loss: $0.01") {
		t.Errorf("expected 'net loss: $0.01' in %q", out)
	}
}
