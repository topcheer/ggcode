package tui

import "github.com/topcheer/ggcode/internal/provider"

// formatUserFacingError converts an agent/provider error into a concise
// human-readable message. Delegates to provider.UserFacingError for the
// heavy lifting.
func formatUserFacingError(lang Language, err error) string {
	if err == nil {
		if lang == LangZhCN {
			return "错误"
		}
		return "Error"
	}
	msg := provider.UserFacingError(err)
	if msg == "" {
		if lang == LangZhCN {
			return "错误"
		}
		return "Error"
	}
	if lang == LangZhCN {
		return "错误: " + msg
	}
	return "Error: " + msg
}
