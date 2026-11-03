package cost

import (
	"encoding/json"
	"strings"
	"sync"
)

type PricingType string

const (
	PricingUnknown      PricingType = ""
	PricingPerToken     PricingType = "per_token"
	PricingSubscription PricingType = "subscription"
	PricingBundled      PricingType = "bundled"
	PricingFree         PricingType = "free"
)

type ModelRate struct {
	InputPerM      float64     `json:"input_per_m,omitempty"`
	OutputPerM     float64     `json:"output_per_m,omitempty"`
	CacheReadPerM  float64     `json:"cache_read_per_m,omitempty"`
	CacheWritePerM float64     `json:"cache_write_per_m,omitempty"`
	Type           PricingType `json:"type,omitempty"`
	Plan           string      `json:"plan,omitempty"`

	CacheReadSet  bool `json:"-"`
	CacheWriteSet bool `json:"-"`
}

func (r *ModelRate) UnmarshalJSON(data []byte) error {
	type rawModelRate ModelRate
	var raw rawModelRate
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*r = ModelRate(raw)
	var probe struct {
		CacheReadPerM  *float64 `json:"cache_read_per_m"`
		CacheWritePerM *float64 `json:"cache_write_per_m"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	r.CacheReadSet = probe.CacheReadPerM != nil
	r.CacheWriteSet = probe.CacheWritePerM != nil
	return nil
}

func (r ModelRate) IsMetered() bool {
	return r.Type == PricingPerToken
}

func (r ModelRate) IsKnown() bool {
	return r.Type != PricingUnknown || r.InputPerM > 0 || r.OutputPerM > 0
}

type PricingTable map[string]map[string]ModelRate

func (t PricingTable) Get(provider, model string) (ModelRate, bool) {
	models, ok := t[provider]
	if !ok {
		models, ok = t[strings.ToLower(provider)]
		if !ok {
			return ModelRate{}, false
		}
	}

	rate, ok := models[model]
	if ok {
		return rate, true
	}

	lowerModel := strings.ToLower(model)
	for k, v := range models {
		if strings.ToLower(k) == lowerModel {
			return v, true
		}
	}

	bestKey := ""
	var bestRate ModelRate
	for k, v := range models {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lowerModel, lk) {
			if len(lk) > len(bestKey) {
				bestKey = lk
				bestRate = v
			}
		}
	}
	if bestKey != "" {
		return bestRate, true
	}

	bestKey = ""
	for k, v := range models {
		lk := strings.ToLower(k)
		if strings.HasSuffix(lowerModel, lk) {
			remaining := strings.TrimSuffix(lowerModel, lk)
			if remaining != "" && !strings.HasSuffix(remaining, "/") {
				continue
			}
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

func IsCodingPlanEndpoint(endpoint string) bool {
	if endpoint == "" {
		return false
	}
	lower := strings.ToLower(endpoint)
	return strings.Contains(lower, "coding") ||
		strings.Contains(lower, "token-plan") ||
		strings.Contains(lower, "token_plan")
}

var subscriptionVendors = map[string]string{
	"aliyun":         "Aliyun Bailian Coding Plan",
	"kimi":           "Kimi Coding Plan",
	"ark":            "Volcengine Ark Coding Plan",
	"minimax":        "MiniMax Token Plan",
	"xiaomi-mimo":    "Xiaomi MiMo Token Plan",
	"github-copilot": "GitHub Copilot",
}

var defaultPricingTable = sync.OnceValue(func() PricingTable {
	return PricingTable{
		"zai": {},

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

		"zhipu": {
			"glm-4.5-air": {Type: PricingFree, Plan: "Zhipu Free Tier"},
		},
	}
})

func DefaultPricingTable() PricingTable {
	return defaultPricingTable()
}

func IsSubscriptionVendor(vendor string) string {
	if plan, ok := subscriptionVendors[strings.ToLower(vendor)]; ok {
		return plan
	}
	return ""
}
