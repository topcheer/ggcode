package cost

// ModelRate holds per-million-token pricing for a single model.
type ModelRate struct {
	InputPerM      float64 `json:"input_per_m"`
	OutputPerM     float64 `json:"output_per_m"`
	CacheReadPerM  float64 `json:"cache_read_per_m"`
	CacheWritePerM float64 `json:"cache_write_per_m"`
}

// PricingTable maps provider+model to pricing rates.
type PricingTable map[string]map[string]ModelRate // provider → model → rate

// Get returns the rate for a provider+model, or false if not found.
func (t PricingTable) Get(provider, model string) (ModelRate, bool) {
	models, ok := t[provider]
	if !ok {
		return ModelRate{}, false
	}
	rate, ok := models[model]
	return rate, ok
}

// Merge overlays another pricing table on top of this one.
func (t PricingTable) Merge(other PricingTable) PricingTable {
	result := make(PricingTable)
	for p, models := range t {
		result[p] = make(map[string]ModelRate)
		for m, rate := range models {
			result[p][m] = rate
		}
	}
	for p, models := range other {
		if result[p] == nil {
			result[p] = make(map[string]ModelRate)
		}
		for m, rate := range models {
			result[p][m] = rate
		}
	}
	return result
}

// DefaultPricingTable returns built-in pricing for known models.
func DefaultPricingTable() PricingTable {
	return PricingTable{
		"anthropic": {
			"claude-sonnet-4-20250514":  {InputPerM: 3.0, OutputPerM: 15.0, CacheReadPerM: 0.30, CacheWritePerM: 3.75},
			"claude-opus-4-20250514":    {InputPerM: 15.0, OutputPerM: 75.0, CacheReadPerM: 1.50, CacheWritePerM: 18.75},
			"claude-haiku-3-5-20241022": {InputPerM: 0.80, OutputPerM: 4.0, CacheReadPerM: 0.08, CacheWritePerM: 1.0},
		},
		"openai": {
			"gpt-4o":      {InputPerM: 2.50, OutputPerM: 10.0, CacheReadPerM: 1.25, CacheWritePerM: 0.0},
			"gpt-4o-mini": {InputPerM: 0.15, OutputPerM: 0.60, CacheReadPerM: 0.075, CacheWritePerM: 0.30},
			"o3":          {InputPerM: 2.0, OutputPerM: 8.0, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
		},
		"gemini": {
			"gemini-2.5-pro":   {InputPerM: 1.25, OutputPerM: 10.0, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"gemini-2.5-flash": {InputPerM: 0.15, OutputPerM: 0.60, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
		},
	}
}
