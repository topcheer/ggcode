package agent

// Sealed-bid marginal-value budget allocator (r441).
//
// Research: 2025-2026 multi-agent systems shift from static uniform resource
// caps to economics-based allocation (contract-net protocol revival,
// sealed-bid auctions, marginal-utility token budgets). r441 grep-confirmed
// ggcode's gap: every budget is a static ceiling applied uniformly -
// tool_call_budget (500-call cap), session_token_budget, token_waste_budget,
// knight/budget - and swarm task assignment is role-matching plus prompt
// self-selection (swarm/manager.go "Only claim tasks that match your role").
// No mechanism anywhere lets competing consumers declare marginal value and
// receive differentiated shares, and no BoN-style race returns resources
// from lost causes to the leader.
//
// This file provides the reusable core, deliberately framework-free:
//
//  1. Allocate - proportional-share sealed-bid allocation over a shared pool
//     (value density = value/cost), with one deficit pass so cheap bids are
//     not starved by rounding and unspent remainder is re-distributed.
//     Deterministic tie-break by AgentID keeps results stable.
//     (The r441 race-yield companion - first-success laggard cancellation -
//     lives in agentruntime/bon_orchestrator.go: package agent must not be
//     imported from agentruntime.)
//
// Zero LLM cost, no I/O - callers supply the signals they already have
// (estimated value, estimated cost, or observed spend such as tool calls).

import (
	"sort"
)

// bidAllocatorMinPool is the smallest pool worth partitioning; below it the
// caller has nothing meaningful to split.
const bidAllocatorMinPool = 1e-9

// Bid is one consumer's sealed declaration for a shared budget pool.
type Bid struct {
	AgentID string
	TaskID  string
	// ValueEstimate is the caller's marginal-utility estimate for getting
	// this task done (any consistent positive scale).
	ValueEstimate float64
	// CostEstimate is the expected consumption from the pool.
	CostEstimate float64
}

// Allocate partitions pool among bids proportionally to value density
// (value/cost), capped at each bid's stated cost, with one deficit pass
// re-distributing whatever the caps left unspent. Bids with non-positive
// value or cost are ignored. Returns nil for an empty/zero pool or when no
// valid bids exist (caller falls back to uniform behavior).
func Allocate(pool float64, bids []Bid) map[string]float64 {
	if pool <= bidAllocatorMinPool || len(bids) == 0 {
		return nil
	}
	valid := make([]Bid, 0, len(bids))
	var densitySum float64
	for _, b := range bids {
		if b.ValueEstimate <= 0 || b.CostEstimate <= 0 || b.AgentID == "" {
			continue
		}
		valid = append(valid, b)
		densitySum += b.ValueEstimate / b.CostEstimate
	}
	if len(valid) == 0 || densitySum <= 0 {
		return nil
	}
	out := make(map[string]float64, len(valid))
	var spent float64
	for _, b := range valid {
		share := pool * (b.ValueEstimate / b.CostEstimate) / densitySum
		alloc := share
		if alloc > b.CostEstimate {
			alloc = b.CostEstimate
		}
		out[b.AgentID] = alloc
		spent += alloc
	}
	// Deficit pass: hand what the caps left over to bids still short of
	// their stated cost, densest first (deterministic order).
	rem := pool - spent
	if rem > bidAllocatorMinPool {
		sort.SliceStable(valid, func(i, j int) bool {
			di, dj := valid[i].ValueEstimate/valid[i].CostEstimate, valid[j].ValueEstimate/valid[j].CostEstimate
			if di != dj {
				return di > dj
			}
			return valid[i].AgentID < valid[j].AgentID
		})
		for _, b := range valid {
			if rem <= bidAllocatorMinPool {
				break
			}
			need := b.CostEstimate - out[b.AgentID]
			if need <= bidAllocatorMinPool {
				continue
			}
			give := need
			if give > rem {
				give = rem
			}
			out[b.AgentID] += give
			rem -= give
		}
	}
	return out
}
