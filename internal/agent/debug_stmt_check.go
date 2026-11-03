package agent

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const maxDebugScanLen = 256 * 1024

var debugLineComment = regexp.MustCompile(`(//|#)[^\n]*`)

var debugBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)

func stripDebugComments(content string) string {
	out := debugBlockComment.ReplaceAllStringFunc(content, func(m string) string {
		return strings.Repeat("\n", strings.Count(m, "\n"))
	})
	out = debugLineComment.ReplaceAllStringFunc(out, func(m string) string {
		return strings.Repeat("\n", strings.Count(m, "\n"))
	})
	return out
}

var debugStmtExemptDirs = []string{
	"testdata/", "fixtures/", "mocks/", "__mocks__/",
	"vendor/", "third_party/", "node_modules/",
}

type leftoverDebugPattern struct {
	name    string
	pattern *regexp.Regexp
	exts    map[string]bool
}

var leftoverDebugPatterns = []leftoverDebugPattern{

	{
		name:    "fmt.Print (Go)",
		pattern: regexp.MustCompile(`\bfmt\.Print(f|ln)?\s*\(`),
		exts:    map[string]bool{".go": true},
	},
	{
		name:    "Go builtin print/println",
		pattern: regexp.MustCompile(`\bprint(ln)?\s*\(`),
		exts:    map[string]bool{".go": true},
	},

	{
		name:    "console.log (JS/TS)",
		pattern: regexp.MustCompile(`\bconsole\.(log|debug|info|warn|error|trace|dir|table|group|groupEnd)\s*\(`),
		exts:    map[string]bool{".js": true, ".jsx": true, ".ts": true, ".tsx": true},
	},
	{
		name:    "debugger statement (JS/TS)",
		pattern: regexp.MustCompile(`\bdebugger\b`),
		exts:    map[string]bool{".js": true, ".jsx": true, ".ts": true, ".tsx": true},
	},

	{
		name:    "pprint (Python)",
		pattern: regexp.MustCompile(`\bpprint(\.pprint)?\s*\(`),
		exts:    map[string]bool{".py": true},
	},
	{
		name:    "breakpoint() (Python)",
		pattern: regexp.MustCompile(`\bbreakpoint\s*\(`),
		exts:    map[string]bool{".py": true},
	},

	{
		name:    "dbg! macro (Rust)",
		pattern: regexp.MustCompile(`\bdbg!`),
		exts:    map[string]bool{".rs": true},
	},

	{
		name:    "var_dump/print_r (PHP)",
		pattern: regexp.MustCompile(`\b(var_dump|print_r|dump)\s*\(`),
		exts:    map[string]bool{".php": true},
	},

	{
		name:    "debugPrint/dump (Swift)",
		pattern: regexp.MustCompile(`\b(debugPrint|dump)\s*\(`),
		exts:    map[string]bool{".swift": true},
	},
}

func checkDebugStmts(filePath, oldContent, newContent string) string {
	if strings.TrimSpace(newContent) == "" {
		return ""
	}

	if !isSourceCodeFile(filePath) {
		return ""
	}

	if isTestFile(filePath) {
		return ""
	}

	lowerPath := strings.ToLower(filePath)
	for _, dir := range debugStmtExemptDirs {
		if strings.Contains(lowerPath, dir) {
			return ""
		}
	}

	scanNew := newContent
	if len(scanNew) > maxDebugScanLen {
		scanNew = scanNew[:maxDebugScanLen]
	}
	scanOld := oldContent
	if len(scanOld) > maxDebugScanLen {
		scanOld = scanOld[:maxDebugScanLen]
	}

	scanNew = stripDebugComments(scanNew)
	scanOld = stripDebugComments(scanOld)

	ext := strings.ToLower(filepath.Ext(filePath))

	var flagged []string

	for _, dp := range leftoverDebugPatterns {

		if dp.exts != nil && !dp.exts[ext] {
			continue
		}

		oldMatches := dp.pattern.FindAllString(scanOld, -1)
		newMatches := dp.pattern.FindAllString(scanNew, -1)

		oldSet := make(map[string]int)
		for _, m := range oldMatches {
			oldSet[m]++
		}
		introduced := 0
		for _, m := range newMatches {
			oldSet[m]--
			if oldSet[m] < 0 {
				introduced++
			}
		}
		if introduced > 0 {
			flagged = append(flagged, fmt.Sprintf("%d x %s", introduced, dp.name))
		}
	}

	if len(flagged) == 0 {
		return ""
	}

	debug.Log("debug-stmt", "detected %d debug-stmt type(s) introduced in %s", len(flagged), filePath)

	return formatDebugStmtWarning(flagged)
}

func formatDebugStmtWarning(flagged []string) string {
	return fmt.Sprintf(
		"[DEBUG STATEMENT WARNING] Detected new debug print statement(s): %s. "+
			"These are typically added during debugging and forgotten. "+
			"Remove them before completing the task, or use a proper logging framework "+
			"if the output is intentional.",
		strings.Join(flagged, ", "))
}

func isTestFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))

	if strings.HasSuffix(base, "_test.go") {
		return true
	}

	testSuffixes := []string{
		".test.js", ".test.jsx", ".test.ts", ".test.tsx",
		".spec.js", ".spec.jsx", ".spec.ts", ".spec.tsx",
	}
	for _, s := range testSuffixes {
		if strings.HasSuffix(base, s) {
			return true
		}
	}

	if strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py") {
		return true
	}
	if strings.HasSuffix(base, "_test.py") {
		return true
	}

	if strings.HasSuffix(base, "_spec.rb") || strings.HasPrefix(base, "test_") {
		return true
	}

	if strings.HasSuffix(base, "test.java") || strings.HasSuffix(base, "tests.java") ||
		strings.HasSuffix(base, "test.kt") || strings.HasSuffix(base, "tests.kt") {
		return true
	}

	if strings.HasSuffix(base, "test.php") {
		return true
	}

	if strings.HasSuffix(base, "_test.rs") {
		return true
	}

	lower := strings.ToLower(path)
	if strings.Contains(lower, "/test/") || strings.Contains(lower, "/tests/") ||
		strings.Contains(lower, "/__tests__/") || strings.Contains(lower, "/spec/") {
		return true
	}
	return false
}
