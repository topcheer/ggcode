package tui

func init() {
	registerCatalog(enPCModule(), zhPCModule())
}

func enPCModule() map[string]string {
	return map[string]string{
		"panel.pc.runtime":                "Runtime",
		"panel.pc.runtime.available":      "available",
		"panel.pc.runtime.not_configured": "not configured (add a PrivateClaw adapter to im.adapters and restart ggcode)",
		// #1731: the connection status used to be hardcoded English - the
		// catalog shipped in the same commit as the panel but was never wired.
		"panel.pc.status.starting":           "starting...",
		"panel.pc.status.connected_none":     "connected (no sessions)",
		"panel.pc.status.connected_sessions": "connected (%d session(s))",
		"panel.pc.status.stopped":            "stopped",
	}
}

func zhPCModule() map[string]string {
	return map[string]string{
		"panel.pc.runtime":                   "运行时",
		"panel.pc.runtime.available":         "可用",
		"panel.pc.runtime.not_configured":    "未配置（在 im.adapters 中添加 PrivateClaw 适配器并重启 ggcode）",
		"panel.pc.status.starting":           "启动中...",
		"panel.pc.status.connected_none":     "已连接（无会话）",
		"panel.pc.status.connected_sessions": "已连接（%d 个会话）",
		"panel.pc.status.stopped":            "已停止",
	}
}
