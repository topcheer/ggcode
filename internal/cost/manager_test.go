package cost

import (
	"encoding/json"
	"os"
	"path/filepath"
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

func TestManagerLoadAllFromDisk(t *testing.T) {
	dir := t.TempDir()

	// Create 3 cost files
	for _, s := range []struct {
		id     string
		tokens int64
	}{
		{"s1", 1000},
		{"s2", 2000},
		{"s3", 3000},
	} {
		sc := SessionCost{
			Provider:     "openai",
			Model:        "gpt-4",
			InputTokens:  s.tokens,
			TotalCostUSD: float64(s.tokens) * 0.00001,
		}
		data, _ := json.Marshal(sc)
		os.WriteFile(filepath.Join(dir, s.id+".cost.json"), data, 0644)
	}
	// Create a corrupt file that should be skipped
	os.WriteFile(filepath.Join(dir, "corrupt.cost.json"), []byte("not json"), 0644)
	// Create a non-cost file that should be ignored
	os.WriteFile(filepath.Join(dir, "other.json"), []byte("{}"), 0644)

	m := NewManager(PricingTable{}, dir)
	loaded := m.LoadAllFromDisk()
	if loaded != 3 {
		t.Fatalf("expected 3 loaded, got %d", loaded)
	}

	// Aggregate should sum all 3
	agg := m.AggregateAllCosts()
	if agg.InputTokens != 6000 {
		t.Errorf("aggregate input tokens: %d, want 6000", agg.InputTokens)
	}
	if agg.TotalCostUSD < 0.059 || agg.TotalCostUSD > 0.061 {
		t.Errorf("aggregate cost: %.4f, want ~0.06", agg.TotalCostUSD)
	}
}

func TestManagerLoadAllFromDiskEmpty(t *testing.T) {
	m := NewManager(PricingTable{}, t.TempDir())
	loaded := m.LoadAllFromDisk()
	if loaded != 0 {
		t.Errorf("expected 0 loaded from empty dir, got %d", loaded)
	}

	agg := m.AggregateAllCosts()
	if agg.InputTokens != 0 || agg.TotalCostUSD != 0 {
		t.Error("expected zero aggregate from empty dir")
	}
}
