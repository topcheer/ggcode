package util

import "unicode/utf8"

// Truncate truncates a string to maxRunes runes, appending "..." if truncated.
// Uses []rune to avoid UTF-8 multi-byte truncation.
func Truncate(s string, maxRunes int) string {
	if maxRunes < 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}

// SnapToRuneStart backs up byte index i to the nearest valid UTF-8 rune
// start boundary at or before i. This prevents producing invalid UTF-8
// when slicing a string at an arbitrary byte position. If i is already at
// a rune boundary, it is returned unchanged.
func SnapToRuneStart(s string, i int) int {
	if i <= 0 {
		return 0
	}
	if i >= len(s) {
		return len(s)
	}
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return i
}
