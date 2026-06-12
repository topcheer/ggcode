package util

// Truncate truncates a string to maxRunes runes, appending "..." if truncated.
// Uses []rune to avoid UTF-8 multi-byte truncation.
func Truncate(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}
