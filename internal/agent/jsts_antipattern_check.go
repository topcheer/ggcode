package agent

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

type jstsAntiPattern struct {
	name             string
	pattern          *regexp.Regexp
	description      string
	tsOnly           bool
	commentDirective bool
}

var jstsAntiPatterns = []jstsAntiPattern{
	{
		name:        "loose equality ==/!=",
		pattern:     regexp.MustCompile(`[^=!<>]==[^=]|[^=!<>]!=[^=]`),
		description: "Loose equality (== or !=) performs type coercion and can cause subtle bugs. Use strict equality (=== or !==) instead.",
	},
	{
		name:        "var declaration",
		pattern:     regexp.MustCompile(`\bvar\s+`),
		description: "var has function-level scoping and hoisting issues. Use const for immutable bindings or let for mutable ones.",
	},
	{
		name:        "explicit any type",
		pattern:     regexp.MustCompile(`:\s*any\b`),
		description: "Explicit `any` type defeats TypeScript's type safety. Consider using `unknown` with type narrowing, or define a proper interface/type.",
		tsOnly:      true,
	},
	{
		name:        "as any type assertion",
		pattern:     regexp.MustCompile(`\bas\s+any\b`),
		description: "`as any` bypasses TypeScript type checking at the call site. Use proper type narrowing, or define an interface for the expected shape.",
		tsOnly:      true,
	},
	{
		name:        "empty catch block",
		pattern:     regexp.MustCompile(`catch\s*(?:\([^)]*\))?\s*\{\s*\}`),
		description: "Empty catch block silently swallows errors. At minimum, log the error or rethrow it. If intentional, add a comment explaining why.",
	},
	{
		name:             "@ts-ignore/@ts-nocheck/@ts-expect-error suppression",
		pattern:          regexp.MustCompile(`@ts-(?:ignore|nocheck|expect-error)`),
		commentDirective: true,
		description:      "@ts-ignore/@ts-nocheck/@ts-expect-error suppresses TypeScript compiler diagnostics, masking potential type errors. Fix the underlying type issue instead.",
		tsOnly:           true,
	},
}

var jstsExemptDirs = []string{
	"node_modules/", "vendor/", "dist/", "build/",
	".min.js", "bundle.js",
}

var jstsExts = map[string]bool{
	".js": true, ".jsx": true, ".mjs": true, ".cjs": true,
	".ts": true, ".tsx": true, ".mts": true, ".cts": true,
}

var tsExts = map[string]bool{
	".ts": true, ".tsx": true, ".mts": true, ".cts": true,
}

func capScanLen(s string) string {
	const maxScan = 256 * 1024
	if len(s) > maxScan {
		return s[:maxScan]
	}
	return s
}

func isExemptJSTSPath(lowerPath string) bool {
	for _, dir := range jstsExemptDirs {
		if strings.Contains(lowerPath, dir) {
			return true
		}
	}
	return false
}

func detectJSTSAntiPatternDeltas(scanOld, scanNew, directiveOld, directiveNew string, isTS bool) []string {
	var flagged []string
	for _, ap := range jstsAntiPatterns {
		if ap.tsOnly && !isTS {
			continue
		}
		oldSrc, newSrc := scanOld, scanNew
		if ap.commentDirective {
			oldSrc, newSrc = directiveOld, directiveNew
		}
		oldCount := len(ap.pattern.FindAllString(oldSrc, -1))
		newCount := len(ap.pattern.FindAllString(newSrc, -1))
		if introduced := newCount - oldCount; introduced > 0 {
			flagged = append(flagged, fmt.Sprintf("%d x %s - %s", introduced, ap.name, ap.description))
		}
	}
	return flagged
}

func checkJSTSAntiPatterns(filePath, oldContent, newContent string) string {
	if strings.TrimSpace(newContent) == "" {
		return ""
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	if !jstsExts[ext] {
		return ""
	}

	if isExemptJSTSPath(strings.ToLower(filePath)) {
		return ""
	}

	flagged := detectJSTSAntiPatternDeltas(
		capScanLen(jstsStripForScan(oldContent)), capScanLen(jstsStripForScan(newContent)),
		capScanLen(jstsStripStringsOnly(oldContent)), capScanLen(jstsStripStringsOnly(newContent)), tsExts[ext],
	)
	if len(flagged) == 0 {
		return ""
	}

	return fmt.Sprintf(
		"[JS/TS Anti-Pattern Warning] Detected %d new anti-pattern(s) introduced by this edit:\n%s",
		len(flagged), strings.Join(flagged, "\n"),
	)
}

func jstsStripForScan(content string) string {
	if content == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	for i, ln := range lines {
		lines[i] = jsStripCommentsAndStrings(ln)
	}
	return strings.Join(lines, "\n")
}

func jstsStripStringsOnly(content string) string {
	if content == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	for i, ln := range lines {
		lines[i] = stripJSStringLiteralsKeepComments(ln)
	}
	return strings.Join(lines, "\n")
}

func stripJSStringLiteralsKeepComments(line string) string {
	var b strings.Builder
	r := []rune(line)
	n := len(r)
	i := 0
	for i < n {
		c := r[i]
		if c == '\'' || c == '"' || c == '`' {
			quote := c
			j := i + 1
			for j < n && r[j] != quote {
				if r[j] == '\\' && j+1 < n {
					j++
				}
				j++
			}
			i = j + 1
			continue
		}
		b.WriteRune(c)
		i++
	}
	return b.String()
}
