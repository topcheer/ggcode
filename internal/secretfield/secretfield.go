// Package secretfield is the single source for the "does this field
// name hold a secret?" heuristic (#2180). Four diverging copies lived
// in config, tui, the provider panel, and the desktop registry - the
// #2167 drift (a fix claiming API_KEY coverage while the pattern
// lacked "key") showed how silently the word lists fork.
package secretfield

import "strings"

// LooksLikeSecretField reports whether a field NAME suggests it holds
// secret material (case-insensitive substring). The wide table covers
// the IM Extra/Env surfaces where arbitrary user keys arrive.
func LooksLikeSecretField(key string) bool {
	lower := strings.ToLower(key)
	for _, pattern := range []string{"secret", "token", "password", "credential", "key"} {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}
