package im

import "github.com/topcheer/ggcode/internal/toollabel"

// imLabelOverrides pins IM-specific strings that intentionally predate the
// r251 shared-table extraction: the IM surface has always rendered the
// skill label as 加载技能/"Load skill" while the TUI uses
// 使用技能/"Using Skill". Byte-preserving this drift keeps IM message text
// stable; the strings themselves are exercised by tool_status_test.go.
var imLabelOverrides = map[toollabel.Lang]map[string]string{
	toollabel.ZhCN: {"skill": "加载技能"},
	toollabel.En:   {"skill": "Load skill"},
}

// sharedLang maps the IM ToolLanguage onto the shared label-table language,
// preserving the legacy default-to-English behavior for all non-zh-CN
// languages.
func sharedLang(lang ToolLanguage) toollabel.Lang {
	if lang == ToolLangZhCN {
		return toollabel.ZhCN
	}
	return toollabel.En
}

// localizedToolLabel and localizedToolActivity delegate to the shared
// table-driven core (internal/toollabel), extracted in r251 from the TUI
// pipeline this file previously mirrored by hand.
func localizedToolLabel(lang ToolLanguage, action string) string {
	l := sharedLang(lang)
	if s, ok := imLabelOverrides[l][action]; ok {
		return s
	}
	return toollabel.Label(l, action)
}

func localizedToolActivity(lang ToolLanguage, action, target string) string {
	return toollabel.Activity(sharedLang(lang), action, target)
}
