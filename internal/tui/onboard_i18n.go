package tui

import "fmt"

// lang returns the current language code based on the user's selection.
func (m *onboardModel) lang() string {
	if m.langCursor < len(m.langs) {
		return m.langs[m.langCursor].code
	}
	return "en"
}

var onboardTexts = map[string]map[string]string{
	"en": {
		"title":            "🚀 ggcode setup",
		"step_language":    "Select your language",
		"lang_en":          "English",
		"lang_zh-CN":       "中文",
		"step_vendor":      "Choose your AI provider",
		"filter":           "Filter:",
		"step_endpoint":    "Configure %s",
		"endpoint_label":   "Endpoint:",
		"apikey_label":     "API Key:",
		"step_model":       "Select model for %s",
		"loading_models":   "Loading models...",
		"step_optional":    "Optional settings (Enter to finish, S to skip)",
		"step_custom":      "Custom provider configuration",
		"custom_protocol":  "Protocol:",
		"custom_name":      "Display Name:",
		"custom_url":       "Base URL:",
		"custom_apikey":    "API Key (optional):",
		"custom_model":     "Model:",
		"custom_submit":    "✓ Continue to optional settings",
		"custom_hint":      "Tab/↑↓ to navigate · ←→ to change protocol · Enter to submit",
		"custom_err_name":  "Display name is required",
		"custom_err_url":   "Base URL is required",
		"custom_err_model": "Model name is required",
		"custom_vendor":    "Custom provider...",
		"permission_mode":  "Permission mode:",
		"mode_supervised":  "confirm before executing",
		"mode_auto":        "auto-execute, confirm risky ops",
		"mode_bypass":      "auto-execute everything",
		"mode_autopilot":   "fully autonomous agent",
		"knight":           "Knight agent:",
		"a2a":              "A2A server:",
		"step_im":          "Configure IM channels (Enter to finish, S to skip)",
		"im_telegram":      "Telegram",
		"im_discord":       "Discord",
		"im_qq":            "QQ",
		"im_wechat":        "WeChat",
		"im_telegram_hint": "Enter Bot Token from @BotFather",
		"im_discord_hint":  "Enter Bot Token from Discord Developer Portal",
		"im_qq_appid":      "App ID:",
		"im_qq_secret":     "App Secret:",
		"im_qq_hint":       "From QQ Open Platform",
		"im_wechat_hint":   "Please configure WeChat via TUI (requires QR scan)",
		"im_more":          "More channels available in TUI",
		"hint_nav":         "↑↓ navigate · Enter select · Tab switch · Esc back · Ctrl+C quit",
		"hint_filter":      "Press / to filter",
		"skip":             "skip",
		"on":               "on",
		"off":              "off",
	},
	"zh-CN": {
		"title":            "🚀 ggcode 初始设置",
		"step_language":    "选择语言",
		"lang_en":          "English",
		"lang_zh-CN":       "中文",
		"step_vendor":      "选择 AI 供应商",
		"filter":           "过滤：",
		"step_endpoint":    "配置 %s",
		"endpoint_label":   "端点：",
		"apikey_label":     "API 密钥：",
		"step_model":       "为 %s 选择模型",
		"loading_models":   "加载模型中...",
		"step_optional":    "可选设置（Enter 完成，S 跳过）",
		"step_custom":      "自定义供应商配置",
		"custom_protocol":  "协议：",
		"custom_name":      "显示名称：",
		"custom_url":       "基础 URL：",
		"custom_apikey":    "API 密钥（可选）：",
		"custom_model":     "模型：",
		"custom_submit":    "✓ 继续到可选设置",
		"custom_hint":      "Tab/↑↓ 导航 · ←→ 切换协议 · Enter 提交",
		"custom_err_name":  "显示名称不能为空",
		"custom_err_url":   "基础 URL 不能为空",
		"custom_err_model": "模型名称不能为空",
		"custom_vendor":    "自定义供应商...",
		"permission_mode":  "权限模式：",
		"mode_supervised":  "执行前确认",
		"mode_auto":        "自动执行，危险操作确认",
		"mode_bypass":      "全部自动执行",
		"mode_autopilot":   "完全自主代理",
		"knight":           "Knight 代理：",
		"a2a":              "A2A 服务：",
		"step_im":          "配置 IM 渠道（Enter 完成，S 跳过）",
		"im_telegram":      "Telegram",
		"im_discord":       "Discord",
		"im_qq":            "QQ",
		"im_wechat":        "微信",
		"im_telegram_hint": "输入 @BotFather 获取的 Bot Token",
		"im_discord_hint":  "输入 Discord Developer Portal 的 Bot Token",
		"im_qq_appid":      "App ID：",
		"im_qq_secret":     "App Secret：",
		"im_qq_hint":       "来自 QQ 开放平台",
		"im_wechat_hint":   "请通过 TUI 配置微信（需要扫码登录）",
		"im_more":          "更多渠道可在 TUI 中配置",
		"hint_nav":         "↑↓ 导航 · Enter 选择 · Tab 切换 · Esc 返回 · Ctrl+C 退出",
		"hint_filter":      "按 / 过滤",
		"skip":             "跳过",
		"on":               "开",
		"off":              "关",
	},
}

// tr returns the translated text for the given key in the current language.
func (m *onboardModel) tr(key string) string {
	lang := m.lang()
	if texts, ok := onboardTexts[lang]; ok {
		if v, ok := texts[key]; ok {
			return v
		}
	}
	if texts, ok := onboardTexts["en"]; ok {
		if v, ok := texts[key]; ok {
			return v
		}
	}
	return key
}

// trf returns a formatted translated text.
func (m *onboardModel) trf(key string, args ...interface{}) string {
	return fmt.Sprintf(m.tr(key), args...)
}
