package tui

func init() {
	registerCatalog(enHomeWarnModule(), zhHomeWarnModule())
}

func enHomeWarnModule() map[string]string {
	return map[string]string{
		"home.warn.title":      "⚠️  You are in your HOME directory",
		"home.warn.message":    "ggcode works best when launched inside a project directory.\nRunning in HOME may expose personal files and produce noisy results.\n\nPlease cd into your project folder and try again.",
		"home.warn.continue":   "Continue anyway",
		"home.warn.exit":       "Exit — I'll cd into a project first",
		"home.warn.shortcut_c": "c",
		"home.warn.shortcut_e": "e",
	}
}

func zhHomeWarnModule() map[string]string {
	return map[string]string{
		"home.warn.title":      "⚠️  你正在 HOME 目录下启动",
		"home.warn.message":    "ggcode 在项目目录下运行效果最佳。\n在 HOME 目录运行可能会暴露个人文件并产生嘈杂的结果。\n\n请先 cd 到你的项目目录再启动。",
		"home.warn.continue":   "仍然继续",
		"home.warn.exit":       "退出 — 我先切换到项目目录",
		"home.warn.shortcut_c": "c",
		"home.warn.shortcut_e": "e",
	}
}
