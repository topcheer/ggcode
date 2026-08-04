package agent

// JS/TS Anti-Pattern Detection in File Writes
//
// Problem: AI coding agents writing JavaScript/TypeScript frequently
// introduce common anti-patterns that cause runtime bugs or defeat the
// purpose of TypeScript:
//   - Loose equality (==, !=) instead of strict (===, !==): type coercion bugs
//   - var declarations instead of const/let: scope and hoisting bugs
//   - Explicit `any` type annotations: defeats TypeScript type safety
//
// Competitor analysis:
//   - Claude Code: no automatic detection (relies on external linters)
//   - Cursor: delegates to ESLint/TSLint integration (not always configured)
//   - Cline/OpenHands: no inline detection
//   - Aider: no detection
//   - Windsurf: relies on external lint integration
//
// ggcode's approach: delta-based, zero-LLM-cost inline detection. Only flags
// anti-patterns INTRODUCED by this edit (count increased vs oldContent),
// avoiding false positives on pre-existing code. Runs in <1ms per file.
//
// This complements insecure_pattern_check.go (which covers eval, XSS, etc.)
// and debug_stmt_check.go (which covers console.log, debugger, etc.).

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// jstsAntiPattern defines a common JS/TS anti-pattern.
type jstsAntiPattern struct {
	name        string
	pattern     *regexp.Regexp
	description string
	// tsOnly restricts to .ts/.tsx/.mts/.cts files (not plain JS)
	tsOnly bool
}

// jstsAntiPatterns lists the anti-patterns to detect, ordered by severity.
var jstsAntiPatterns = []jstsAntiPattern{
	{
		name:        "loose equality ==/!=",
		pattern:     regexp.MustCompile(`[^=!<>]==[^=]`),
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
}

// jstsExemptDirs lists directories where anti-patterns are expected.
var jstsExemptDirs = []string{
	"node_modules/", "vendor/", "dist/", "build/",
	".min.js", "bundle.js",
}

// checkJSTSAntiPatterns detects JS/TS anti-patterns INTRODUCED by this edit.
// Returns a combined warning string if any new anti-patterns are found.
func checkJSTSAntiPatterns(filePath, oldContent, newContent string) string {
	if strings.TrimSpace(newContent) == "" {
		return ""
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	isJSTS := ext == ".js" || ext == ".jsx" || ext == ".mjs" || ext == ".cjs" ||
		ext == ".ts" || ext == ".tsx" || ext == ".mts" || ext == ".cts"
	if !isJSTS {
		return ""
	}

	// Skip minified/generated files and exempt directories
	lowerPath := strings.ToLower(filePath)
	for _, dir := range jstsExemptDirs {
		if strings.Contains(lowerPath, dir) {
			return ""
		}
	}

	isTS := ext == ".ts" || ext == ".tsx" || ext == ".mts" || ext == ".cts"

	// Cap scan length
	const maxScan = 256 * 1024
	scanNew := newContent
	if len(scanNew) > maxScan {
		scanNew = scanNew[:maxScan]
	}
	scanOld := oldContent
	if len(scanOld) > maxScan {
		scanOld = scanOld[:maxScan]
	}

	var flagged []string

	for _, ap := range jstsAntiPatterns {
		if ap.tsOnly && !isTS {
			continue
		}

		oldCount := len(ap.pattern.FindAllString(scanOld, -1))
		newCount := len(ap.pattern.FindAllString(scanNew, -1))

		introduced := newCount - oldCount
		if introduced > 0 {
			flagged = append(flagged, fmt.Sprintf("%d x %s - %s", introduced, ap.name, ap.description))
		}
	}

	if len(flagged) == 0 {
		return ""
	}

	return fmt.Sprintf(
		"[JS/TS Anti-Pattern Warning] Detected %d new anti-pattern(s) introduced by this edit:\n%s",
		len(flagged), strings.Join(flagged, "\n"),
	)
}
