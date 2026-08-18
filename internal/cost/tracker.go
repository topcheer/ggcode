package cost

import (
	"sort"
	"sync"
)

// SessionCost tracks cumulative token usage and estimated cost for a session.
type SessionCost struct {
	Provider         string  `json:"provider"`
	Model            string  `json:"model"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	TotalCostUSD     float64 `json:"total_cost_usd"`

	// HasPricing reports whether pricing data was available for the
	// provider/model when TotalCostUSD was last computed. #683: without it,
	// unknown models displayed a false-precise $0.0000 indistinguishable from
	// a genuinely free session, and persisted .cost.json snapshots never
	// recalculated even after pricing became available.
	HasPricing bool `json:"has_pricing,omitempty"`
}

// AgentCostEntry tracks token usage attributable to a single sub-agent,
// teammate, or delegation within a session. This enables per-agent cost
// isolation — knowing exactly which sub-agent consumed what resources.
//
// Competitor mapping:
//   - Cursor: per-agent cost breakdown in background tasks panel
//   - Claude Code: per-subtask token attribution
//   - OpenHands: per-agent budget enforcement
//
// The "main" agentID (empty string) represents the primary agent loop.
// Sub-agent IDs like "spawn:abc123" or "teammate:tm-2" isolate their costs.
type AgentCostEntry struct {
	AgentID      string  `json:"agent_id"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CacheRead    int64   `json:"cache_read_tokens"`
	CacheWrite   int64   `json:"cache_write_tokens"`
	TotalCostUSD float64 `json:"total_cost_usd"`
}

// AgentCostBreakdown is a sorted list of per-agent cost entries, returned
// for reporting and budget enforcement.
type AgentCostBreakdown struct {
	Entries []AgentCostEntry `json:"entries"`
	Total   SessionCost      `json:"total"`
}

// Tracker accumulates token usage and computes cost.
type Tracker struct {
	mu         sync.Mutex
	cost       SessionCost
	pricing    PricingTable
	agentCosts map[string]*AgentCostEntry // agentID → accumulated usage
}

// NewTracker creates a cost tracker for the given provider/model.
func NewTracker(provider, model string, pricing PricingTable) *Tracker {
	return &Tracker{
		cost:       SessionCost{Provider: provider, Model: model},
		pricing:    pricing,
		agentCosts: make(map[string]*AgentCostEntry),
	}
}

// Record adds a usage update from an API call.
func (t *Tracker) Record(usage TokenUsage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cost.InputTokens += int64(usage.InputTokens)
	t.cost.OutputTokens += int64(usage.OutputTokens)
	t.cost.CacheReadTokens += int64(usage.CacheRead)
	t.cost.CacheWriteTokens += int64(usage.CacheWrite)
	t.recalculate()
}

// RecordForAgent adds a usage update attributable to a specific sub-agent.
// agentID identifies the source: "" for the main agent loop, "spawn:<id>"
// for spawned sub-agents, "teammate:<id>" for swarm teammates, "a2a:<name>"
// for remote delegations. The usage is also added to the session total.
func (t *Tracker) RecordForAgent(agentID string, usage TokenUsage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cost.InputTokens += int64(usage.InputTokens)
	t.cost.OutputTokens += int64(usage.OutputTokens)
	t.cost.CacheReadTokens += int64(usage.CacheRead)
	t.cost.CacheWriteTokens += int64(usage.CacheWrite)
	t.recalculate()
	t.recordAgentLocked(agentID, usage)
}

// recordAgentLocked updates the per-agent breakdown. Caller must hold t.mu.
func (t *Tracker) recordAgentLocked(agentID string, usage TokenUsage) {
	if t.agentCosts == nil {
		t.agentCosts = make(map[string]*AgentCostEntry)
	}
	entry, ok := t.agentCosts[agentID]
	if !ok {
		entry = &AgentCostEntry{AgentID: agentID}
		t.agentCosts[agentID] = entry
	}
	entry.InputTokens += int64(usage.InputTokens)
	entry.OutputTokens += int64(usage.OutputTokens)
	entry.CacheRead += int64(usage.CacheRead)
	entry.CacheWrite += int64(usage.CacheWrite)

	rate, ok := t.pricing.Get(t.cost.Provider, t.cost.Model)
	if ok {
		// #529: use the shared fallback (0.10x/1.25x input) so cache tokens are
		// never billed at zero when the pricing table omits explicit cache
		// fields, and the tracker agrees with analyzeCacheLocked.
		cacheReadPerM, cacheWritePerM := effectiveCacheRates(rate)
		entry.TotalCostUSD =
			float64(entry.InputTokens)*rate.InputPerM/1e6 +
				float64(entry.OutputTokens)*rate.OutputPerM/1e6 +
				float64(entry.CacheRead)*cacheReadPerM/1e6 +
				float64(entry.CacheWrite)*cacheWritePerM/1e6
	}
}

// AgentCostBreakdown returns a per-agent cost breakdown sorted by cost
// descending. The "main" agent (empty agentID) is labeled "main" in the
// output. This enables cost visibility for sub-agents, teammates, and
// remote delegations.
func (t *Tracker) AgentCostBreakdown() AgentCostBreakdown {
	t.mu.Lock()
	defer t.mu.Unlock()

	result := AgentCostBreakdown{Total: t.cost}
	if len(t.agentCosts) == 0 {
		return result
	}

	result.Entries = make([]AgentCostEntry, 0, len(t.agentCosts))
	for id, e := range t.agentCosts {
		entry := *e
		if id == "" {
			entry.AgentID = "main"
		}
		result.Entries = append(result.Entries, entry)
	}

	// Sort by cost descending, then by total tokens as tiebreaker.
	sort.Slice(result.Entries, func(i, j int) bool {
		if result.Entries[i].TotalCostUSD != result.Entries[j].TotalCostUSD {
			return result.Entries[i].TotalCostUSD > result.Entries[j].TotalCostUSD
		}
		iTokens := result.Entries[i].InputTokens + result.Entries[i].OutputTokens
		jTokens := result.Entries[j].InputTokens + result.Entries[j].OutputTokens
		return iTokens > jTokens
	})
	return result
}

// AgentCost returns the cost attributed to a specific agentID.
// Returns false if no cost has been recorded for that agent.
func (t *Tracker) AgentCost(agentID string) (AgentCostEntry, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	entry, ok := t.agentCosts[agentID]
	if !ok {
		return AgentCostEntry{}, false
	}
	return *entry, true
}

// SessionCost returns a snapshot of the current cost.
func (t *Tracker) SessionCost() SessionCost {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cost
}

func (t *Tracker) recalculate() {
	rate, ok := t.pricing.Get(t.cost.Provider, t.cost.Model)
	if !ok {
		// #683: mark the session as having no pricing data instead of leaving
		// a silent $0.00 that display layers rendered as a real cost. The
		// persisted total (if any) is preserved so a loaded snapshot is never
		// zeroed — display layers show "(no pricing data)" instead.
		t.cost.HasPricing = false
		return
	}
	t.cost.HasPricing = true
	// #529: same shared cache-rate fallback as recordAgentLocked/analyzeCacheLocked.
	cacheReadPerM, cacheWritePerM := effectiveCacheRates(rate)
	t.cost.TotalCostUSD =
		float64(t.cost.InputTokens)*rate.InputPerM/1e6 +
			float64(t.cost.OutputTokens)*rate.OutputPerM/1e6 +
			float64(t.cost.CacheReadTokens)*cacheReadPerM/1e6 +
			float64(t.cost.CacheWriteTokens)*cacheWritePerM/1e6
}
