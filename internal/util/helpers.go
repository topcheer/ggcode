package util

import "strings"

// FirstNonEmpty returns the first non-empty string from the given values.
// Strings that are empty or contain only whitespace are skipped.
// Returns the trimmed value of the first match, or "" if all values are empty.
func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
