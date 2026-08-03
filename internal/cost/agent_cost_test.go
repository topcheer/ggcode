package cost

import (
	"strings"
	"testing"
)

func TestRecordForAgent_Attribution(t *testing.T) {
	tr := NewTracker("openai", "gpt-4", testPricing())

	// Main agent uses 1000 input, 500 output.
	tr.RecordForAgent("", TokenUsage{InputTokens: 1000, OutputTokens: 500})
	// Sub-agent "spawn:res-1" uses 2000 input, 1000 output.
	tr.RecordForAgent("spawn:res-1", TokenUsage{InputTokens: 2000, OutputTokens: 1000})

	sc := tr.SessionCost()
	if sc.InputTokens != 3000 {
		t.Errorf("total input: want 3000, got %d", sc.InputTokens)
	}
	if sc.OutputTokens != 1500 {
		t.Errorf("total output: want 1500, got %d", sc.OutputTokens)
	}

	// Check per-agent breakdown.
	main, ok := tr.AgentCost("")
	if !ok {
		t.Fatal("expected main agent cost entry")
	}
	if main.InputTokens != 1000 {
		t.Errorf("main input: want 1000, got %d", main.InputTokens)
	}

	spawn, ok := tr.AgentCost("spawn:res-1")
	if !ok {
		t.Fatal("expected spawn:res-1 agent cost entry")
	}
	if spawn.InputTokens != 2000 {
		t.Errorf("spawn input: want 2000, got %d", spawn.InputTokens)
	}
	if spawn.OutputTokens != 1000 {
		t.Errorf("spawn output: want 1000, got %d", spawn.OutputTokens)
	}
}

func TestAgentCostBreakdown_SortedByCost(t *testing.T) {
	tr := NewTracker("openai", "gpt-4", testPricing())

	// Main agent: cheaper.
	tr.RecordForAgent("", TokenUsage{InputTokens: 100, OutputTokens: 50})
	// Sub-agent: more expensive.
	tr.RecordForAgent("spawn:heavy", TokenUsage{InputTokens: 5000, OutputTokens: 3000})

	breakdown := tr.AgentCostBreakdown()
	if len(breakdown.Entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(breakdown.Entries))
	}

	// Sorted by cost descending: spawn:heavy should be first.
	if breakdown.Entries[0].AgentID != "spawn:heavy" {
		t.Errorf("expected spawn:heavy first, got %s", breakdown.Entries[0].AgentID)
	}
	if breakdown.Entries[1].AgentID != "main" {
		t.Errorf("expected main second, got %s", breakdown.Entries[1].AgentID)
	}
}

func TestAgentCostBreakdown_MultipleAgents(t *testing.T) {
	tr := NewTracker("anthropic", "claude-3-opus", testPricing())

	tr.RecordForAgent("", TokenUsage{InputTokens: 1000, OutputTokens: 500})
	tr.RecordForAgent("spawn:res-1", TokenUsage{InputTokens: 800, OutputTokens: 400})
	tr.RecordForAgent("spawn:res-2", TokenUsage{InputTokens: 600, OutputTokens: 300})
	tr.RecordForAgent("teammate:tm-2", TokenUsage{InputTokens: 500, OutputTokens: 250})

	breakdown := tr.AgentCostBreakdown()
	if len(breakdown.Entries) != 4 {
		t.Fatalf("want 4 entries, got %d", len(breakdown.Entries))
	}

	// Verify total matches sum of entries.
	var totalInput int64
	for _, e := range breakdown.Entries {
		totalInput += e.InputTokens
	}
	if totalInput != breakdown.Total.InputTokens {
		t.Errorf("sum of entry inputs (%d) != total (%d)", totalInput, breakdown.Total.InputTokens)
	}
}

func TestAgentCostBreakdown_Empty(t *testing.T) {
	tr := NewTracker("openai", "gpt-4", testPricing())
	breakdown := tr.AgentCostBreakdown()
	if len(breakdown.Entries) != 0 {
		t.Errorf("want 0 entries for fresh tracker, got %d", len(breakdown.Entries))
	}
}

func TestAgentCostBreakdown_OnlyMainAgent(t *testing.T) {
	tr := NewTracker("openai", "gpt-4", testPricing())
	// Record without per-agent attribution (plain Record).
	tr.Record(TokenUsage{InputTokens: 1000, OutputTokens: 500})

	breakdown := tr.AgentCostBreakdown()
	// Plain Record does not populate per-agent data.
	if len(breakdown.Entries) != 0 {
		t.Errorf("want 0 entries when only Record() used, got %d", len(breakdown.Entries))
	}
}

func TestRecordForAgent_CumulativeUpdates(t *testing.T) {
	tr := NewTracker("openai", "gpt-4", testPricing())

	// Same agent makes multiple API calls.
	tr.RecordForAgent("spawn:multi", TokenUsage{InputTokens: 100, OutputTokens: 50})
	tr.RecordForAgent("spawn:multi", TokenUsage{InputTokens: 200, OutputTokens: 100})
	tr.RecordForAgent("spawn:multi", TokenUsage{InputTokens: 300, OutputTokens: 150})

	entry, ok := tr.AgentCost("spawn:multi")
	if !ok {
		t.Fatal("expected entry for spawn:multi")
	}
	if entry.InputTokens != 600 {
		t.Errorf("cumulative input: want 600, got %d", entry.InputTokens)
	}
	if entry.OutputTokens != 300 {
		t.Errorf("cumulative output: want 300, got %d", entry.OutputTokens)
	}
}

func TestFormatAgentCostBreakdown(t *testing.T) {
	tr := NewTracker("openai", "gpt-4", testPricing())
	tr.RecordForAgent("", TokenUsage{InputTokens: 1000, OutputTokens: 500})
	tr.RecordForAgent("spawn:res-1", TokenUsage{InputTokens: 2000, OutputTokens: 1000})

	breakdown := tr.AgentCostBreakdown()
	out := FormatAgentCostBreakdown(breakdown)
	if out == "" {
		t.Fatal("expected non-empty output")
	}
	if !strings.Contains(out, "Per-agent cost breakdown") {
		t.Errorf("missing header: %s", out)
	}
	if !strings.Contains(out, "main") {
		t.Errorf("missing main agent: %s", out)
	}
	if !strings.Contains(out, "spawn:res-1") {
		t.Errorf("missing spawn:res-1: %s", out)
	}
	if !strings.Contains(out, "%") {
		t.Errorf("missing percentage: %s", out)
	}
}

func TestFormatAgentCostBreakdown_Empty(t *testing.T) {
	tr := NewTracker("openai", "gpt-4", testPricing())
	breakdown := tr.AgentCostBreakdown()
	if out := FormatAgentCostBreakdown(breakdown); out != "" {
		t.Errorf("expected empty for no entries, got %s", out)
	}
}

func TestManager_GetAgentCostBreakdown(t *testing.T) {
	m := NewManager(testPricing(), "")
	tr := m.GetOrCreateTracker("sess-1", "openai", "gpt-4")
	tr.RecordForAgent("", TokenUsage{InputTokens: 1000, OutputTokens: 500})
	tr.RecordForAgent("spawn:res-1", TokenUsage{InputTokens: 500, OutputTokens: 200})

	breakdown, ok := m.GetAgentCostBreakdown("sess-1")
	if !ok {
		t.Fatal("expected breakdown for sess-1")
	}
	if len(breakdown.Entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(breakdown.Entries))
	}

	// Non-existent session.
	_, ok = m.GetAgentCostBreakdown("sess-2")
	if ok {
		t.Error("expected false for non-existent session")
	}
}

// testPricing returns a simple pricing table for testing.
func testPricing() PricingTable {
	return DefaultPricingTable()
}
