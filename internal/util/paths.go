package util

// RelativizePaths is kept for API compatibility but no longer performs
// path replacement. Absolute paths are shown as-is in all surfaces.
func RelativizePaths(text, _ string) string {
	return text
}

// FormatToolDetail prepares a tool detail string for display.
// No relativization or truncation — the rendering layer handles width.
func FormatToolDetail(text, _ string) string {
	return text
}
