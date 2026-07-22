package config

import "strings"

// builtinVendorDisplayZh maps built-in vendor IDs to Chinese display names.
// Only vendors with meaningful Chinese translations are listed here.
// International brand names (Anthropic, OpenAI, etc.) are not translated.
var builtinVendorDisplayZh = map[string]string{
	"zai":            "智谱 Z.AI",
	"xiaomi-mimo":    "小米 MiMo",
	"moonshot":       "月之暗面",
	"aliyun":         "阿里云百炼",
	"kimi":           "Kimi",
	"minimax":        "MiniMax",
	"ark":            "火山引擎方舟",
	"github-copilot": "GitHub Copilot",
	"ai-gateway":     "AI 网关",
}

// builtinEndpointDisplayZh maps "vendorID/endpointID" to Chinese display names.
// The key format is "vendor/endpoint" to avoid collisions across vendors.
var builtinEndpointDisplayZh = map[string]string{
	// ZAI
	"zai/cn-coding-openai":        "国内编程套餐",
	"zai/cn-coding-anthropic":     "国内编程套餐 (Anthropic)",
	"zai/global-coding-openai":    "国际编程套餐",
	"zai/global-coding-anthropic": "国际编程套餐 (Anthropic)",
	"zai/cn-api-openai":           "国内标准 API",
	"zai/global-api-openai":       "国际标准 API",
	// Aliyun
	"aliyun/coding-openai":    "百炼编程套餐",
	"aliyun/coding-anthropic": "百炼编程套餐 (Anthropic)",
	// Kimi
	"kimi/coding-openai":    "Kimi 编程套餐",
	"kimi/coding-anthropic": "Kimi 编程套餐 (Anthropic)",
	// MiniMax
	"minimax/token-plan-openai":    "MiniMax 套餐",
	"minimax/token-plan-anthropic": "MiniMax 套餐 (Anthropic)",
	"minimax/global-openai":        "MiniMax 国际",
	"minimax/global-anthropic":     "MiniMax 国际 (Anthropic)",
	// Ark
	"ark/coding-openai":    "方舟编程套餐",
	"ark/coding-anthropic": "方舟编程套餐 (Anthropic)",
	// XiaoMi
	"xiaomi-mimo/cn-openai":    "小米 MiMo API",
	"xiaomi-mimo/cn-anthropic": "小米 MiMo API (Anthropic)",
	// GitHub Copilot
	"github-copilot/github.com": "GitHub.com",
	"github-copilot/enterprise": "GitHub 企业版",
	// AI Gateway
	"ai-gateway/aihubmix":   "AIHubMix",
	"ai-gateway/getgoapi":   "GetGoAPI",
	"ai-gateway/novita":     "Novita AI",
	"ai-gateway/nvidia":     "NVIDIA NIM",
	"ai-gateway/openrouter": "OpenRouter",
	"ai-gateway/poe":        "Poe",
	"ai-gateway/requesty":   "Requesty",
	"ai-gateway/together":   "Together AI",
	"ai-gateway/perplexity": "Perplexity",
	"ai-gateway/vercel":     "Vercel AI 网关",
}

// isChineseLanguage returns true if the language code indicates Chinese.
func isChineseLanguage(lang string) bool {
	return strings.HasPrefix(lang, "zh")
}

// localizedVendorDisplay returns the localized display name for a built-in vendor.
// Falls back to the English display name if no translation exists.
func localizedVendorDisplay(vendorID, englishName, lang string) string {
	if isChineseLanguage(lang) {
		if zh, ok := builtinVendorDisplayZh[vendorID]; ok {
			return zh
		}
	}
	return englishName
}

// localizedEndpointDisplay returns the localized display name for a built-in endpoint.
// Falls back to the English display name if no translation exists.
func localizedEndpointDisplay(vendorID, endpointID, englishName, lang string) string {
	if isChineseLanguage(lang) {
		key := vendorID + "/" + endpointID
		if zh, ok := builtinEndpointDisplayZh[key]; ok {
			return zh
		}
	}
	return englishName
}
