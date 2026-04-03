package cost

// SessionCost tracks cumulative token usage and estimated cost for a session.
type SessionCost struct {
	Provider        string  `json:"provider"`
	Model           string  `json:"model"`
	InputTokens     int64   `json:"input_tokens"`
	OutputTokens    int64   `json:"output_tokens"`
	CacheReadTokens int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	TotalCostUSD    float64 `json:"total_cost_usd"`
}

// Tracker accumulates token usage and computes cost.
type Tracker struct {
	cost    SessionCost
	pricing PricingTable
}

// NewTracker creates a cost tracker for the given provider/model.
func NewTracker(provider, model string, pricing PricingTable) *Tracker {
	return &Tracker{
		cost: SessionCost{Provider: provider, Model: model},
		pricing: pricing,
	}
}

// Record adds a usage update from an API call.
func (t *Tracker) Record(usage TokenUsage) {
	t.cost.InputTokens += int64(usage.InputTokens)
	t.cost.OutputTokens += int64(usage.OutputTokens)
	t.cost.CacheReadTokens += int64(usage.CacheRead)
	t.cost.CacheWriteTokens += int64(usage.CacheWrite)
	t.recalculate()
}

// SessionCost returns a snapshot of the current cost.
func (t *Tracker) SessionCost() SessionCost {
	return t.cost
}

func (t *Tracker) recalculate() {
	rate, ok := t.pricing.Get(t.cost.Provider, t.cost.Model)
	if !ok {
		return
	}
	t.cost.TotalCostUSD =
		float64(t.cost.InputTokens)*rate.InputPerM/1e6 +
		float64(t.cost.OutputTokens)*rate.OutputPerM/1e6 +
		float64(t.cost.CacheReadTokens)*rate.CacheReadPerM/1e6 +
		float64(t.cost.CacheWriteTokens)*rate.CacheWritePerM/1e6
}
