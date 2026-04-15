package tui

func init() {
	registerCatalog(enPCModule(), zhPCModule())
}

func enPCModule() map[string]string {
	return map[string]string{
		"panel.pc.runtime":                "Runtime",
		"panel.pc.runtime.available":      "available",
		"panel.pc.runtime.not_configured": "not configured (add a PrivateClaw adapter to im.adapters and restart ggcode)",
	}
}

func zhPCModule() map[string]string {
	return map[string]string{
		"panel.pc.runtime":                "运行时",
		"panel.pc.runtime.available":      "可用",
		"panel.pc.runtime.not_configured": "未配置（在 im.adapters 中添加 PrivateClaw 适配器并重启 ggcode）",
	}
}
