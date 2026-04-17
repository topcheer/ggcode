package util

import (
	"path/filepath"
	"strings"
)

// RelativizePaths replaces absolute paths under baseDir with relative paths ("./").
// It handles two cases:
//   - baseDir + separator + suffix → "./" + suffix
//   - baseDir as an exact standalone token → "."
//
// The second case uses boundary checking to avoid false matches
// (e.g. "/Users/proj" should not match inside "/Users/proj-backup").
func RelativizePaths(text, baseDir string) string {
	if baseDir == "" || text == "" {
		return text
	}
	wd := filepath.Clean(baseDir)
	wdSlash := wd + string(filepath.Separator)

	// Case 1: baseDir/... → ./...
	text = strings.ReplaceAll(text, wdSlash, "./")

	// Case 2: exact baseDir (not followed by a path separator).
	// ReplaceAll would also match "/proj" inside "/proj-backup", so we
	// scan for boundary-safe occurrences instead.
	var b strings.Builder
	i := 0
	for {
		idx := strings.Index(text[i:], wd)
		if idx < 0 {
			b.WriteString(text[i:])
			break
		}
		idx += i
		b.WriteString(text[i:idx])
		after := idx + len(wd)
		// Only replace when the character after the match is not a path
		// component character (case 1 already handled the separator case).
		if after >= len(text) || !isPathChar(text[after]) {
			b.WriteString(".")
		} else {
			b.WriteString(wd)
		}
		i = after
	}
	return b.String()
}

// isPathChar returns true if the byte could be part of a path component
// (alphanumeric, underscore, dash, or dot).
func isPathChar(c byte) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '_' || c == '-' || c == '.'
}
