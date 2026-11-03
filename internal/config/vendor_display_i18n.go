package config

import "strings"

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

var builtinEndpointDisplayZh = map[string]string{

	"zai/cn-coding-openai":        "国内编程套餐",
	"zai/cn-coding-anthropic":     "国内编程套餐 (Anthropic)",
	"zai/global-coding-openai":    "国际编程套餐",
	"zai/global-coding-anthropic": "国际编程套餐 (Anthropic)",
	"zai/cn-api-openai":           "国内标准 API",
	"zai/global-api-openai":       "国际标准 API",

	"aliyun/coding-openai":    "百炼编程套餐",
	"aliyun/coding-anthropic": "百炼编程套餐 (Anthropic)",

	"kimi/coding-openai":    "Kimi 编程套餐",
	"kimi/coding-anthropic": "Kimi 编程套餐 (Anthropic)",

	"minimax/token-plan-openai":    "MiniMax 套餐",
	"minimax/token-plan-anthropic": "MiniMax 套餐 (Anthropic)",
	"minimax/global-openai":        "MiniMax 国际",
	"minimax/global-anthropic":     "MiniMax 国际 (Anthropic)",

	"ark/coding-openai":    "方舟编程套餐",
	"ark/coding-anthropic": "方舟编程套餐 (Anthropic)",

	"xiaomi-mimo/cn-openai":    "小米 MiMo API",
	"xiaomi-mimo/cn-anthropic": "小米 MiMo API (Anthropic)",

	"github-copilot/github.com": "GitHub.com",
	"github-copilot/enterprise": "GitHub 企业版",

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

func isChineseLanguage(lang string) bool {
	return strings.HasPrefix(lang, "zh")
}

func localizedVendorDisplay(vendorID, englishName, lang string) string {
	if isChineseLanguage(lang) {
		if zh, ok := builtinVendorDisplayZh[vendorID]; ok {
			return zh
		}
	}
	return englishName
}

func localizedEndpointDisplay(vendorID, endpointID, englishName, lang string) string {
	if isChineseLanguage(lang) {
		key := vendorID + "/" + endpointID
		if zh, ok := builtinEndpointDisplayZh[key]; ok {
			return zh
		}
	}
	return englishName
}

func LocalizedVendorDisplay(vendorID, englishName, lang string) string {
	return localizedVendorDisplay(vendorID, englishName, lang)
}

func LocalizedEndpointDisplay(vendorID, endpointID, englishName, lang string) string {
	return localizedEndpointDisplay(vendorID, endpointID, englishName, lang)
}
