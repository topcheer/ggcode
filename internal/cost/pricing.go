package cost

import "strings"

// PricingType indicates how a model is billed.
type PricingType string

const (
	// PricingUnknown: no pricing data available. Display "(no pricing data)".
	// This is the default — we do NOT hardcode per-token prices because
	// model pricing changes frequently and stale data is worse than no data.
	PricingUnknown PricingType = ""
	// PricingPerToken: standard per-million-token billing.
	// Only set when the user has explicitly configured pricing via Merge().
	PricingPerToken PricingType = "per_token"
	// PricingSubscription: flat-rate subscription with monthly quota.
	// Display "included in <Plan>" instead of dollar amount.
	PricingSubscription PricingType = "subscription"
	// PricingBundled: included in a bundle (e.g., Google One AI Premium).
	PricingBundled PricingType = "bundled"
	// PricingFree: free tier, no cost.
	PricingFree PricingType = "free"
)

// ModelRate holds pricing info for a single model.
// For subscription/bundled/free models, the per-token fields are zero
// and PricingType indicates the billing model.
type ModelRate struct {
	InputPerM      float64     `json:"input_per_m,omitempty"`
	OutputPerM     float64     `json:"output_per_m,omitempty"`
	CacheReadPerM  float64     `json:"cache_read_per_m,omitempty"`
	CacheWritePerM float64     `json:"cache_write_per_m,omitempty"`
	Type           PricingType `json:"type,omitempty"`
	Plan           string      `json:"plan,omitempty"` // e.g., "GitHub Copilot", "GLM Coding Plan"
}

// IsMetered returns true if the model is billed per-token.
func (r ModelRate) IsMetered() bool {
	return r.Type == PricingPerToken
}

// IsKnown returns true if any pricing information is available.
func (r ModelRate) IsKnown() bool {
	return r.Type != PricingUnknown || r.InputPerM > 0 || r.OutputPerM > 0
}

// PricingTable maps provider+model to pricing info.
type PricingTable map[string]map[string]ModelRate // provider → model → rate

// Get returns the rate for a provider+model, or false if not found.
// Uses fuzzy matching for versioned model names.
func (t PricingTable) Get(provider, model string) (ModelRate, bool) {
	models, ok := t[provider]
	if !ok {
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

	// Prefix match (longest wins for specificity)
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

	// Suffix match (longest wins)
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

// IsCodingPlanEndpoint returns true if the endpoint name indicates a coding plan.
// Coding plan endpoints use subscription billing (monthly quota), not per-token.
// Examples: "cn-coding-openai", "global-coding-anthropic", "kimi-coding", "ark-coding"
func IsCodingPlanEndpoint(endpoint string) bool {
	if endpoint == "" {
		return false
	}
	lower := strings.ToLower(endpoint)
	return strings.Contains(lower, "coding") ||
		strings.Contains(lower, "token-plan") ||
		strings.Contains(lower, "token_plan")
}

// subscriptionVendors is the set of vendors whose built-in config is
// entirely subscription-based (all endpoints are coding/token plans).
var subscriptionVendors = map[string]string{
	// vendor key → plan display name
	// Verified via online research, see sources below.
	"aliyun":         "Aliyun Bailian Coding Plan", // https://www.aliyun.com/benefit/scene/codingplan
	"kimi":           "Kimi Coding Plan",           // https://www.kimi.com/zh-cn/help/membership/membership-pricing
	"ark":            "Volcengine Ark Coding Plan", // https://www.volcengine.com/activity/codingplan
	"minimax":        "MiniMax Token Plan",         // https://platform.minimaxi.com/docs/guides/pricing-token-plan
	"xiaomi-mimo":    "Xiaomi MiMo Token Plan",     // https://mimo.mi.com/docs/zh-CN/tokenplan/subscription
	"github-copilot": "GitHub Copilot",             // https://github.com/features/copilot/plans
}

// DefaultPricingTable returns built-in billing-type info for known providers.
//
// IMPORTANT: We deliberately do NOT hardcode per-token prices. Model pricing
// changes frequently and stale data is misleading. This table only contains
// billing TYPE information (subscription vs per-token vs free) that is
// verifiable from each provider's official pricing page.
//
// For per-token pricing, users can configure custom rates via Merge().
func DefaultPricingTable() PricingTable {
	return PricingTable{
		// Z.ai: both coding plan (subscription) and standard API (per-token).
		// The coding plan endpoints (cn-coding-*, global-coding-*) are subscription.
		// The standard API endpoints (cn-api-*, global-api-*) are per-token.
		// We mark all zai models as unknown here — the actual billing type
		// is determined by the endpoint in IsCodingPlanEndpoint().
		// Source: https://z.ai/subscribe (Lite/Pro/Max plans from $18/mo)
		"zai": {},

		// GitHub Copilot: $10/mo (Pro), $19/user/mo (Business).
		// All models included in subscription.
		// Source: https://github.com/features/copilot/plans
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

		// GLM-4-Air: permanently free model on Zhipu/ZAI platform.
		// Source: https://open.bigmodel.cn/pricing
		"zhipu": {
			"glm-4.5-air": {Type: PricingFree, Plan: "Zhipu Free Tier"},
		},
	}
}

// IsSubscriptionVendor returns the plan name if the vendor is entirely
// subscription-based (all built-in endpoints are coding/token plans).
// Returns empty string if the vendor is not a subscription vendor.
func IsSubscriptionVendor(vendor string) string {
	if plan, ok := subscriptionVendors[strings.ToLower(vendor)]; ok {
		return plan
	}
	return ""
}
