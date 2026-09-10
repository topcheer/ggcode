package tui

// #1732: this module previously carried 73 keys, but the en/zh MAIN
// catalogs switch on these same keys BEFORE falling back to module
// lookup - 66 of them were unreachable for every language (the default
// branch consults modules only after the main switch misses). Only the
// seven claude/anthropic_oauth keys below exist solely here. Shadowed
// duplicates were a stale-value trap: editing them here did nothing
// while the handlers evolved in the main catalogs.

func init() {
	registerCatalog(enProviderModule(), zhProviderModule())
}

func enProviderModule() map[string]string {
	return map[string]string{
		"panel.provider.hint.anthropic_oauth":      "Anthropic OAuth: l login • x logout",
		"panel.provider.login.claude_starting":     "Starting Anthropic login...",
		"panel.provider.login.claude_instructions": "Opening browser for Anthropic login...",
		"panel.provider.login.claude_manual":       "If browser didn't open, visit: %s",
		"panel.provider.login.claude_success":      "Anthropic connected.",
		"panel.provider.login.claude_failed":       "Anthropic login failed: %s",
		"panel.provider.logout.claude_success":     "Anthropic disconnected.",
	}
}

func zhProviderModule() map[string]string {
	return map[string]string{
		"panel.provider.hint.anthropic_oauth":      "Anthropic OAuth：l 登录 • x 登出",
		"panel.provider.login.claude_starting":     "正在启动 Anthropic 登录...",
		"panel.provider.login.claude_instructions": "正在打开浏览器进行 Anthropic 登录...",
		"panel.provider.login.claude_manual":       "如果浏览器未打开，请访问：%s",
		"panel.provider.login.claude_success":      "Anthropic 已连接。",
		"panel.provider.login.claude_failed":       "Anthropic 登录失败：%s",
		"panel.provider.logout.claude_success":     "Anthropic 已断开。",
	}
}
