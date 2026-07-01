package cost

import "strings"

// PricingType indicates how a model is billed.
type PricingType string

const (
	// PricingPerToken: standard per-million-token billing (default).
	PricingPerToken PricingType = "per_token"
	// PricingSubscription: flat-rate subscription, no per-token cost.
	// Display "included in plan" instead of dollar amount.
	PricingSubscription PricingType = "subscription"
	// PricingBundled: included in a bundle (e.g., Google One AI Premium).
	// Same as subscription but with different display wording.
	PricingBundled PricingType = "bundled"
	// PricingFree: free tier, no cost.
	PricingFree PricingType = "free"
)

// ModelRate holds per-million-token pricing for a single model.
// For subscription/bundled/free models, the per-token fields are zero
// and PricingType indicates the billing model.
type ModelRate struct {
	InputPerM      float64     `json:"input_per_m"`
	OutputPerM     float64     `json:"output_per_m"`
	CacheReadPerM  float64     `json:"cache_read_per_m"`
	CacheWritePerM float64     `json:"cache_write_per_m"`
	Type           PricingType `json:"type,omitempty"`
	Plan           string      `json:"plan,omitempty"` // e.g., "GitHub Copilot", "Claude Max"
}

// IsMetered returns true if the model is billed per-token.
func (r ModelRate) IsMetered() bool {
	return r.Type == "" || r.Type == PricingPerToken
}

// PricingTable maps provider+model to pricing rates.
type PricingTable map[string]map[string]ModelRate // provider → model → rate

// Get returns the rate for a provider+model, or false if not found.
// Tries exact match first, then prefix/wildcard match for versioned models.
func (t PricingTable) Get(provider, model string) (ModelRate, bool) {
	models, ok := t[provider]
	if !ok {
		// Try lowercase provider
		models, ok = t[strings.ToLower(provider)]
		if !ok {
			return ModelRate{}, false
		}
	}

	// Exact match
	rate, ok := models[model]
	if ok {
		return rate, true
	}

	// Case-insensitive match
	lowerModel := strings.ToLower(model)
	for k, v := range models {
		if strings.ToLower(k) == lowerModel {
			return v, true
		}
	}

	// Prefix match (handles versioned model names like "gpt-4o-2024-08-06")
	// Pick the longest matching prefix for specificity (gpt-4o wins over gpt-4).
	bestKey := ""
	var bestRate ModelRate
	for k, v := range models {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lowerModel, lk) || strings.HasPrefix(lk, lowerModel) {
			if len(lk) > len(bestKey) {
				bestKey = lk
				bestRate = v
			}
		}
	}
	if bestKey != "" {
		return bestRate, true
	}

	// Suffix match (handles "anthropic/claude-sonnet-4" matching "claude-sonnet-4")
	// Pick the longest matching suffix for specificity.
	bestKey = ""
	for k, v := range models {
		lk := strings.ToLower(k)
		if strings.HasSuffix(lowerModel, lk) {
			if len(lk) > len(bestKey) {
				bestKey = lk
				bestRate = v
			}
		}
	}
	if bestKey != "" {
		return bestRate, true
	}

	return ModelRate{}, false
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
// Prices are per million tokens in USD, sourced from provider pricing pages.
func DefaultPricingTable() PricingTable {
	return PricingTable{
		"anthropic": {
			// Claude 4 family
			"claude-sonnet-4-20250514": {InputPerM: 3.0, OutputPerM: 15.0, CacheReadPerM: 0.30, CacheWritePerM: 3.75},
			"claude-opus-4-20250514":   {InputPerM: 15.0, OutputPerM: 75.0, CacheReadPerM: 1.50, CacheWritePerM: 18.75},
			"claude-sonnet-4":          {InputPerM: 3.0, OutputPerM: 15.0, CacheReadPerM: 0.30, CacheWritePerM: 3.75},
			"claude-opus-4":            {InputPerM: 15.0, OutputPerM: 75.0, CacheReadPerM: 1.50, CacheWritePerM: 18.75},
			// Claude 3.5 family
			"claude-haiku-3-5-20241022": {InputPerM: 0.80, OutputPerM: 4.0, CacheReadPerM: 0.08, CacheWritePerM: 1.0},
			"claude-3-5-sonnet":         {InputPerM: 3.0, OutputPerM: 15.0, CacheReadPerM: 0.30, CacheWritePerM: 3.75},
			"claude-3-5-haiku":          {InputPerM: 0.80, OutputPerM: 4.0, CacheReadPerM: 0.08, CacheWritePerM: 1.0},
			// Claude 3 family
			"claude-3-opus": {InputPerM: 15.0, OutputPerM: 75.0, CacheReadPerM: 1.50, CacheWritePerM: 18.75},
		},
		"openai": {
			"gpt-4o":      {InputPerM: 2.50, OutputPerM: 10.0, CacheReadPerM: 1.25, CacheWritePerM: 0.0},
			"gpt-4o-mini": {InputPerM: 0.15, OutputPerM: 0.60, CacheReadPerM: 0.075, CacheWritePerM: 0.30},
			"gpt-4-turbo": {InputPerM: 10.0, OutputPerM: 30.0, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"gpt-4":       {InputPerM: 30.0, OutputPerM: 60.0, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"gpt-3.5":     {InputPerM: 0.50, OutputPerM: 1.50, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"o1":          {InputPerM: 15.0, OutputPerM: 60.0, CacheReadPerM: 7.50, CacheWritePerM: 0.0},
			"o3":          {InputPerM: 2.0, OutputPerM: 8.0, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"o3-mini":     {InputPerM: 1.10, OutputPerM: 4.40, CacheReadPerM: 0.55, CacheWritePerM: 0.0},
			"o4-mini":     {InputPerM: 1.10, OutputPerM: 4.40, CacheReadPerM: 0.55, CacheWritePerM: 0.0},
		},
		"gemini": {
			"gemini-2.5-pro":   {InputPerM: 1.25, OutputPerM: 10.0, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"gemini-2.5-flash": {InputPerM: 0.15, OutputPerM: 0.60, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"gemini-2.0-flash": {InputPerM: 0.10, OutputPerM: 0.40, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"gemini-1.5-pro":   {InputPerM: 1.25, OutputPerM: 5.0, CacheReadPerM: 0.3125, CacheWritePerM: 0.0},
			"gemini-1.5-flash": {InputPerM: 0.075, OutputPerM: 0.30, CacheReadPerM: 0.01875, CacheWritePerM: 0.0},
		},
		"deepseek": {
			"deepseek-chat":     {InputPerM: 0.27, OutputPerM: 1.10, CacheReadPerM: 0.07, CacheWritePerM: 0.27},
			"deepseek-reasoner": {InputPerM: 0.55, OutputPerM: 2.19, CacheReadPerM: 0.14, CacheWritePerM: 0.55},
			"deepseek-coder":    {InputPerM: 0.14, OutputPerM: 0.28, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
		},
		"moonshot": {
			"moonshot-v1-8k":   {InputPerM: 1.60, OutputPerM: 3.20, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"moonshot-v1-32k":  {InputPerM: 3.30, OutputPerM: 6.60, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"moonshot-v1-128k": {InputPerM: 8.60, OutputPerM: 17.20, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
		},
		"kimi": {
			"kimi-for-coding": {InputPerM: 1.60, OutputPerM: 3.20, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
		},
		"groq": {
			"llama-3.1-8b-instant":    {InputPerM: 0.05, OutputPerM: 0.08, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"llama-3.3-70b-versatile": {InputPerM: 0.59, OutputPerM: 0.79, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"mixtral-8x7b":            {InputPerM: 0.24, OutputPerM: 0.24, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"gemma2-9b-it":            {InputPerM: 0.20, OutputPerM: 0.20, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
		},
		"mistral": {
			"mistral-small-latest": {InputPerM: 0.20, OutputPerM: 0.60, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"mistral-large-latest": {InputPerM: 2.00, OutputPerM: 6.00, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"codestral-latest":     {InputPerM: 0.30, OutputPerM: 0.90, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
		},
		"zhipu": {
			"glm-4-plus":  {InputPerM: 0.70, OutputPerM: 0.70, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"glm-4":       {InputPerM: 0.50, OutputPerM: 0.50, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"glm-4-flash": {InputPerM: 0.10, OutputPerM: 0.10, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"glm-4-air":   {InputPerM: 0.001, OutputPerM: 0.001, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"glm-4-turbo": {InputPerM: 0.70, OutputPerM: 0.70, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"glm-5-turbo": {InputPerM: 1.40, OutputPerM: 1.40, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
		},
		"zai": {
			"glm-5-turbo": {InputPerM: 1.40, OutputPerM: 1.40, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"glm-4-plus":  {InputPerM: 0.70, OutputPerM: 0.70, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"glm-4":       {InputPerM: 0.50, OutputPerM: 0.50, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
		},
		"minimax": {
			"abab6.5s-chat": {InputPerM: 0.43, OutputPerM: 0.43, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"abab6.5-chat":  {InputPerM: 2.90, OutputPerM: 8.60, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
		},
		"ark": {
			"doubao-pro-32k":  {InputPerM: 0.11, OutputPerM: 0.28, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"doubao-pro-128k": {InputPerM: 0.43, OutputPerM: 1.15, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"doubao-lite-32k": {InputPerM: 0.003, OutputPerM: 0.006, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
		},
		"together": {
			"llama-3.3-70b": {InputPerM: 0.88, OutputPerM: 0.88, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"qwen2.5-72b":   {InputPerM: 0.88, OutputPerM: 0.88, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"deepseek-v3":   {InputPerM: 1.25, OutputPerM: 1.25, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
		},
		"perplexity": {
			"sonar-pro": {InputPerM: 3.0, OutputPerM: 15.0, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
			"sonar":     {InputPerM: 1.0, OutputPerM: 1.0, CacheReadPerM: 0.0, CacheWritePerM: 0.0},
		},
		// GitHub Copilot — flat-rate subscription, no per-token cost.
		// Copilot Pro = $10/mo, Copilot Business = $19/user/mo.
		// All models listed in copilot vendor defaults are included.
		"github-copilot": {
			"claude-haiku-4.5":       {Type: PricingSubscription, Plan: "GitHub Copilot"},
			"claude-opus-4.5":        {Type: PricingSubscription, Plan: "GitHub Copilot"},
			"claude-opus-4.7":        {Type: PricingSubscription, Plan: "GitHub Copilot"},
			"claude-sonnet-4.5":      {Type: PricingSubscription, Plan: "GitHub Copilot"},
			"claude-sonnet-4.6":      {Type: PricingSubscription, Plan: "GitHub Copilot"},
			"gemini-2.5-pro":         {Type: PricingSubscription, Plan: "GitHub Copilot"},
			"gemini-3-flash-preview": {Type: PricingSubscription, Plan: "GitHub Copilot"},
			"gemini-3.1-pro-preview": {Type: PricingSubscription, Plan: "GitHub Copilot"},
			"gpt-3.5-turbo":          {Type: PricingSubscription, Plan: "GitHub Copilot"},
			"gpt-4":                  {Type: PricingSubscription, Plan: "GitHub Copilot"},
			"gpt-4-0125-preview":     {Type: PricingSubscription, Plan: "GitHub Copilot"},
			"gpt-4.1":                {Type: PricingSubscription, Plan: "GitHub Copilot"},
			"gpt-4o":                 {Type: PricingSubscription, Plan: "GitHub Copilot"},
			"gpt-4o-mini":            {Type: PricingSubscription, Plan: "GitHub Copilot"},
			"o1":                     {Type: PricingSubscription, Plan: "GitHub Copilot"},
			"o3":                     {Type: PricingSubscription, Plan: "GitHub Copilot"},
			"o3-mini":                {Type: PricingSubscription, Plan: "GitHub Copilot"},
			"o4-mini":                {Type: PricingSubscription, Plan: "GitHub Copilot"},
		},
	}
}
