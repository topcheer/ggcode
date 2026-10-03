package agent

import "testing"

// r441 probes: sealed-bid marginal-value allocator. Pure-function coverage:
// density proportionality, cost caps, deficit redistribution, degenerate
// inputs, determinism.

func TestAllocateDensityProportional(t *testing.T) {
	bids := []Bid{
		{AgentID: "a", ValueEstimate: 100, CostEstimate: 10}, // density 10
		{AgentID: "b", ValueEstimate: 100, CostEstimate: 50}, // density 2
	}
	alloc := Allocate(300, bids)
	if alloc == nil {
		t.Fatal("nil allocation")
	}
	// Densities 10:2 -> raw shares 250:50; both under cost caps (10,50)??
	// a is capped at 10; the 240 remainder flows to b (deficit pass, only
	// bidder still short of its 50 cap... b gets 50 total, 190 stays
	// unspent - Allocate never exceeds stated costs).
	if alloc["a"] != 10 {
		t.Errorf("a = %v, want 10 (cost cap)", alloc["a"])
	}
	if alloc["b"] != 50 {
		t.Errorf("b = %v, want 50 (deficit pass up to cap)", alloc["b"])
	}
	var total float64
	for _, v := range alloc {
		total += v
	}
	if total > 300+1e-9 {
		t.Errorf("total %v exceeds pool 300", total)
	}
}

func TestAllocateNoCapsBind(t *testing.T) {
	// No cap binds: allocation is exactly density-proportional.
	bids := []Bid{
		{AgentID: "a", ValueEstimate: 30, CostEstimate: 10}, // density 3
		{AgentID: "b", ValueEstimate: 10, CostEstimate: 10}, // density 1
	}
	alloc := Allocate(100, bids) // shares 75:25, caps 10 each -> both capped!
	// Both capped at 10 -> total 20, remainder unspent (no infinite chase).
	if alloc["a"] != 10 || alloc["b"] != 10 {
		t.Errorf("caps must bind: a=%v b=%v, want 10/10", alloc["a"], alloc["b"])
	}
}

func TestAllocateBigPoolDensitySplit(t *testing.T) {
	// Costs large enough that caps never bind: pure density split.
	bids := []Bid{
		{AgentID: "a", ValueEstimate: 300, CostEstimate: 100}, // density 3
		{AgentID: "b", ValueEstimate: 100, CostEstimate: 100}, // density 1
	}
	alloc := Allocate(80, bids) // shares 60:20
	if alloc["a"] != 60 || alloc["b"] != 20 {
		t.Errorf("density split: a=%v b=%v, want 60/20", alloc["a"], alloc["b"])
	}
}

func TestAllocateDegenerate(t *testing.T) {
	if Allocate(0, []Bid{{AgentID: "a", ValueEstimate: 1, CostEstimate: 1}}) != nil {
		t.Error("zero pool must return nil")
	}
	if Allocate(100, nil) != nil {
		t.Error("no bids must return nil")
	}
	bad := []Bid{
		{AgentID: "", ValueEstimate: 1, CostEstimate: 1},  // missing ID
		{AgentID: "b", ValueEstimate: 0, CostEstimate: 1}, // no value
		{AgentID: "c", ValueEstimate: 1, CostEstimate: 0}, // no cost
	}
	if Allocate(100, bad) != nil {
		t.Error("invalid bids only must return nil")
	}
	// Single valid bid gets min(pool, cost).
	got := Allocate(50, []Bid{{AgentID: "solo", ValueEstimate: 5, CostEstimate: 80}})
	if got["solo"] != 50 {
		t.Errorf("solo = %v, want 50", got["solo"])
	}
}

func TestAllocateDeterministic(t *testing.T) {
	bids := []Bid{
		{AgentID: "x", ValueEstimate: 10, CostEstimate: 5},
		{AgentID: "y", ValueEstimate: 10, CostEstimate: 5},
	}
	a1 := Allocate(100, bids)
	a2 := Allocate(100, bids)
	for k := range a1 {
		if a1[k] != a2[k] {
			t.Errorf("nondeterministic for %s: %v vs %v", k, a1[k], a2[k])
		}
	}
}
