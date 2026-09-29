package tui

import "github.com/topcheer/ggcode/internal/toollabel"

// sharedLang maps the TUI Language onto the shared label-table language.
// The legacy dispatch switches treated every non-zh-CN language as English
// (default branch); this preserves that exactly.
func sharedLang(lang Language) toollabel.Lang {
	if lang == LangZhCN {
		return toollabel.ZhCN
	}
	return toollabel.En
}

// localizedToolLabel and localizedToolActivity delegate to the shared
// table-driven core (internal/toollabel), extracted in r251 from two
// drifting per-package copies (tui CC 166/142, im CC 58/30). Output is
// pinned byte-for-byte by the existing tool_labels golden tests.
func localizedToolLabel(lang Language, action string) string {
	return toollabel.Label(sharedLang(lang), action)
}

func localizedToolActivity(lang Language, action, target string) string {
	return toollabel.Activity(sharedLang(lang), action, target)
}
