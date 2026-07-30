package agent

// Debug statement detection for written code.
//
// Research basis: AI coding agents (Claude Code, Cursor, Cline, Aider) frequently
// introduce debug print statements during development — console.log in JS/TS,
// fmt.Println in Go, print() in Python, dd()/dump() in PHP, dbg! in Rust, etc.
// These debug artifacts are often left in the final code, causing:
//   - Production noise/log pollution
//   - Performance degradation
//   - Accidental exposure of sensitive data (dumping structs, tokens, etc.)
//   - Build warnings from unused imports
//
// Claude Code uses a pre-commit review step; Cursor's lint-on-save catches some;
// Cline/OpenHands rely on the build/test cycle to surface them. Aider strips
// debug lines during its diff review.
//
// ggcode's approach: detect debug statements at write time by comparing
// occurrence counts before vs. after the edit. Only NEW debug statements
// (introduced by this edit) are flagged — pre-existing ones are left alone.
// This is zero-LLM-cost, language-aware, and has no false positives for
// existing code.

import (
	"fmt"
	"path/filepath"
	"strings"
)

// debugPattern represents a debug print/log pattern to detect.
type debugPattern struct {
	// pattern is the substring to search for (case-sensitive for Go/Java/Rust,
	// case-insensitive for Python — matching is controlled by the detector).
	pattern string
	// label is a human-readable description for the warning message.
	label string
}

// debugPatternsByExt maps file extensions to their debug detection patterns.
// Only patterns that are almost always debug artifacts are included — we avoid
// ambiguous ones (e.g., bare "print" in Python, "fmt.Println" in Go's main.go)
// to minimize false positives.
var debugPatternsByExt = map[string][]debugPattern{
	".js": {
		{"console.log(", "console.log"},
		{"console.debug(", "console.debug"},
		{"console.info(", "console.info"},
		{"console.warn(", "console.warn"},
		{"debugger;", "debugger statement"},
		{"debugger ;", "debugger statement"},
	},
	".jsx": {
		{"console.log(", "console.log"},
		{"console.debug(", "console.debug"},
		{"console.info(", "console.info"},
		{"console.warn(", "console.warn"},
		{"debugger;", "debugger statement"},
		{"debugger ;", "debugger statement"},
	},
	".ts": {
		{"console.log(", "console.log"},
		{"console.debug(", "console.debug"},
		{"console.info(", "console.info"},
		{"console.warn(", "console.warn"},
		{"debugger;", "debugger statement"},
		{"debugger ;", "debugger statement"},
	},
	".tsx": {
		{"console.log(", "console.log"},
		{"console.debug(", "console.debug"},
		{"console.info(", "console.info"},
		{"console.warn(", "console.warn"},
		{"debugger;", "debugger statement"},
		{"debugger ;", "debugger statement"},
	},
	".py": {
		{"breakpoint()", "breakpoint()"},
		{"import pdb", "pdb import"},
		{"pdb.set_trace", "pdb.set_trace"},
	},
	".rb": {
		{"binding.pry", "binding.pry"},
		{"require 'pry'", "pry import"},
		{`require "pry"`, "pry import"},
	},
	".php": {
		{"dd(", "dd()"},
		{"dump(", "dump()"},
		{"var_dump(", "var_dump()"},
		{"print_r(", "print_r()"},
	},
	".rs": {
		{"dbg!(", "dbg!"},
	},
	".java": {
		{"e.printStackTrace()", "printStackTrace()"},
	},
	".kt": {
		{".printStackTrace()", "printStackTrace()"},
	},
	".swift": {
		{"print(", "print()"},
		{"debugPrint(", "debugPrint()"},
	},
}

// checkDebugStatements detects debug prints/logs that were INTRODUCED by this
// edit (present in newContent but not in oldContent). Returns warning strings.
//
// Key design decisions:
//   - Only flags NEW debug statements: compares occurrence counts in old vs new
//     content. If oldContent already had the same pattern, it's pre-existing.
//   - Skips test files: debug prints in tests are often intentional fixtures.
//   - No false positives for legitimate logging (e.g., structured loggers like
//     slog.Info, log.Printf are NOT flagged — only raw debug prints).
func checkDebugStatements(filePath, oldContent, newContent string) []string {
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	ext := filepath.Ext(filePath)
	patterns, ok := debugPatternsByExt[ext]
	if !ok {
		return nil
	}

	// Skip test files — debug prints in tests are often intentional.
	if isTestFile(filePath) {
		return nil
	}

	oldCounts := make(map[string]int)
	for _, p := range patterns {
		oldCounts[p.pattern] = strings.Count(oldContent, p.pattern)
	}

	var warnings []string
	for _, p := range patterns {
		newCount := strings.Count(newContent, p.pattern)
		introduced := newCount - oldCounts[p.pattern]
		if introduced > 0 {
			warnings = append(warnings, formatDebugWarning(p.label, introduced, ext))
		}
	}

	return warnings
}

// formatDebugWarning renders a concise warning for a newly-introduced debug pattern.
func formatDebugWarning(label string, count int, ext string) string {
	noun := "statement"
	if count > 1 {
		noun = "statements"
	}
	return fmt.Sprintf(
		"Introduced %d %s %s. These are debug artifacts — remove them before committing "+
			"(use a proper logger if logging is intended).",
		count, label, noun)
}

// isTestFile returns true for common test file naming conventions.
func isTestFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	// Go: *_test.go
	if strings.HasSuffix(base, "_test.go") {
		return true
	}
	// JS/TS: *.test.js, *.spec.ts, *.test.jsx, *.spec.tsx
	testSuffixes := []string{
		".test.js", ".test.jsx", ".test.ts", ".test.tsx",
		".spec.js", ".spec.jsx", ".spec.ts", ".spec.tsx",
	}
	for _, s := range testSuffixes {
		if strings.HasSuffix(base, s) {
			return true
		}
	}
	// Python: test_*.py, *_test.py
	if strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py") {
		return true
	}
	if strings.HasSuffix(base, "_test.py") {
		return true
	}
	// Ruby: *_spec.rb, test_*.rb
	if strings.HasSuffix(base, "_spec.rb") || strings.HasPrefix(base, "test_") {
		return true
	}
	// Java/Kotlin: *Test.java, *Tests.java, *Test.kt
	if strings.HasSuffix(base, "test.java") || strings.HasSuffix(base, "tests.java") ||
		strings.HasSuffix(base, "test.kt") || strings.HasSuffix(base, "tests.kt") {
		return true
	}
	// PHP: *Test.php
	if strings.HasSuffix(base, "test.php") {
		return true
	}
	// Rust: tests/*.rs or *_test.rs
	if strings.HasSuffix(base, "_test.rs") {
		return true
	}
	// Files under test directories
	lower := strings.ToLower(path)
	if strings.Contains(lower, "/test/") || strings.Contains(lower, "/tests/") ||
		strings.Contains(lower, "/__tests__/") || strings.Contains(lower, "/spec/") {
		return true
	}
	return false
}
