package cost

import (
	"os"
	"testing"
	"time"
)

func TestManagerSessionCost(t *testing.T) {
	m := NewManager(PricingTable{}, t.TempDir())

	// Not found
	_, ok := m.SessionCost("nonexistent")
	if ok {
		t.Error("expected not found")
	}

	// Create and retrieve
	tr := m.GetOrCreateTracker("s1", "openai", "gpt-4")
	tr.Record(TokenUsage{InputTokens: 100, OutputTokens: 50})

	sc, ok := m.SessionCost("s1")
	if !ok {
		t.Fatal("expected found")
	}
	if sc.InputTokens != 100 {
		t.Errorf("input tokens: %d", sc.InputTokens)
	}
}

func TestManagerSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(PricingTable{}, dir)

	tr := m.GetOrCreateTracker("s1", "openai", "gpt-4")
	tr.Record(TokenUsage{InputTokens: 200, OutputTokens: 100})

	if err := m.Save("s1"); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	// Save nonexistent session should not error
	if err := m.Save("nonexistent"); err != nil {
		t.Fatalf("Save nonexistent error: %v", err)
	}

	// Load into new manager
	m2 := NewManager(PricingTable{}, dir)
	m2.Load("s1", "openai", "gpt-4")

	sc, ok := m2.SessionCost("s1")
	if !ok {
		t.Fatal("expected loaded session")
	}
	if sc.InputTokens != 200 {
		t.Errorf("input tokens after load: %d", sc.InputTokens)
	}
}

func TestFormatSessionCostOutput(t *testing.T) {
	sc := SessionCost{
		Provider:     "openai",
		Model:        "gpt-4",
		InputTokens:  1500,
		OutputTokens: 500,
		TotalCostUSD: 0.05,
	}
	result := FormatSessionCost(sc, time.Now())
	if result == "" {
		t.Error("expected non-empty result")
	}
}

func TestManagerLoadCorrupt(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(dir+"/s1.cost.json", []byte("not json"), 0644)

	m := NewManager(PricingTable{}, dir)
	m.Load("s1", "p1", "m1") // should not panic
}

func TestManagerLoadNonexistent(t *testing.T) {
	m := NewManager(PricingTable{}, t.TempDir())
	m.Load("nonexistent", "p1", "m1") // should not panic
}
