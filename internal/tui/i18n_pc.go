package tui

func init() {
	registerCatalog(enPCModule(), zhPCModule())
}

func enPCModule() map[string]string {
	return map[string]string{
		// #1731: the connection status used to be hardcoded English - the
		// catalog shipped in the same commit as the panel but was never wired.
		"panel.pc.status.starting":           "starting...",
		"panel.pc.status.connected_none":     "connected (no sessions)",
		"panel.pc.status.connected_sessions": "connected (%d session(s))",
		"panel.pc.status.stopped":            "stopped",
		"panel.pc.status.error":              "error: %s",
		"panel.pc.status.raw":                "%s",
	}
}

func zhPCModule() map[string]string {
	return map[string]string{
		"panel.pc.status.starting":           "启动中...",
		"panel.pc.status.connected_none":     "已连接（无会话）",
		"panel.pc.status.connected_sessions": "已连接（%d 个会话）",
		"panel.pc.status.stopped":            "已停止",
		"panel.pc.status.error":              "错误：%s",
		"panel.pc.status.raw":                "%s",
	}
}
