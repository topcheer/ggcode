package agent

// Debug Statement Detection in File Writes
//
// Problem: AI coding agents frequently add temporary debug print statements
// (fmt.Println, console.log, print(), debugger) while troubleshooting an issue,
// then forget to remove them before completing the task. These leftover
// statements pollute production code, leak internal state to logs/stdout, and
// create noise in code review.
//
// Competitor analysis:
//   - Claude Code: no automatic detection (relies on agent self-judgment)
//   - Cursor: no automatic detection
//   - Cline/OpenHands: no automatic detection
//   - Aider: no automatic detection
//   - Windsurf: no automatic detection
//   - GitHub Copilot: sometimes warns via lint integration
//
// ggcode's approach: delta-based detection -- only flag debug statements
// INTRODUCED by this edit (count in newContent > count in oldContent).
// This avoids false positives on pre-existing debug output and focuses
// exclusively on NEW debug statements the agent just added. The check is
// zero-LLM-cost and <1ms per file.
//
// This is DIFFERENT from debug_sniffer.go:
//   - debug_sniffer.go: detects debug output in COMMAND RUN RESULTS (runtime)
//     to suggest the agent look at debug output
//   - THIS module: detects debug statements WRITTEN to source files on disk
//     (leftover-code prevention, code-quality flow)

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// maxDebugScanLen caps the content size scanned for debug statements.
const maxDebugScanLen = 256 * 1024

// debugStmtExemptDirs lists directory patterns where debug statements are
// expected and should NOT trigger warnings.
var debugStmtExemptDirs = []string{
	"testdata/", "fixtures/", "mocks/", "__mocks__/",
	"vendor/", "third_party/", "node_modules/",
}

// leftoverDebugPattern defines a language-specific debug print pattern.
type leftoverDebugPattern struct {
	name    string
	pattern *regexp.Regexp
	exts    map[string]bool // nil = all source files; otherwise restrict to these extensions
}

// leftoverDebugPatterns are ordered by language. Each uses word boundaries or
// function-call syntax to minimize false positives.
var leftoverDebugPatterns = []leftoverDebugPattern{
	// --- Go ---
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

	// --- JavaScript / TypeScript ---
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

	// --- Python ---
	{
		name:    "print() (Python)",
		pattern: regexp.MustCompile(`\bprint\s*\(`),
		exts:    map[string]bool{".py": true},
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

	// --- Rust ---
	{
		name:    "println!/print!/eprintln! (Rust)",
		pattern: regexp.MustCompile(`\b(println|print|eprintln|eprint|dbg)!`),
		exts:    map[string]bool{".rs": true},
	},

	// --- Ruby ---
	// Note: bare `p` is an extremely common Ruby variable name (Proc, person,
	// point). Only match actual debug calls: p(obj), puts(x), pp(x), warn(msg),
	// or string-argument forms like puts "text" / p "text" (#112).
	{
		name:    "puts/p/pp (Ruby)",
		pattern: regexp.MustCompile(`\b(puts|pp|p|warn)\s*[\({]|\b(puts|pp|p|warn)\s+"`),
		exts:    map[string]bool{".rb": true},
	},

	// --- Java / Kotlin ---
	{
		name:    "System.out/err (Java/Kotlin)",
		pattern: regexp.MustCompile(`\bSystem\.(out|err)\.(print|println|printf)\s*\(`),
		exts:    map[string]bool{".java": true, ".kt": true},
	},

	// --- C / C++ ---
	{
		name:    "printf/fprintf (C/C++)",
		pattern: regexp.MustCompile(`\b(printf|fprintf|puts|fputs|cout|cerr)\s*[<(]`),
		exts:    map[string]bool{".c": true, ".cpp": true, ".cc": true, ".h": true, ".hpp": true},
	},

	// --- PHP ---
	{
		name:    "var_dump/print_r (PHP)",
		pattern: regexp.MustCompile(`\b(var_dump|print_r|dump)\s*\(`),
		exts:    map[string]bool{".php": true},
	},

	// --- Dart ---
	{
		name:    "print (Dart)",
		pattern: regexp.MustCompile(`\bprint\s*\(`),
		exts:    map[string]bool{".dart": true},
	},

	// --- Swift ---
	{
		name:    "print/debugPrint (Swift)",
		pattern: regexp.MustCompile(`\b(print|debugPrint|dump)\s*\(`),
		exts:    map[string]bool{".swift": true},
	},
}

// checkDebugStmts detects debug print statements that were INTRODUCED by this
// edit (present in newContent but not in oldContent). Returns a single
// combined warning string if any new debug statements are found.
//
// Design decisions:
//   - Only flags NEW debug statements: compares occurrence counts.
//   - Only checks source code files.
//   - Skips test files (where prints may be intentional) and fixture dirs.
//   - Language-scoped: each pattern only matches its own language's extensions.
func checkDebugStmts(filePath, oldContent, newContent string) string {
	if strings.TrimSpace(newContent) == "" {
		return ""
	}

	// Only check source code files
	if !isSourceCodeFile(filePath) {
		return ""
	}

	// Skip test files — debug prints in tests are often intentional
	if isTestFile(filePath) {
		return ""
	}

	// Skip exempt directories
	lowerPath := strings.ToLower(filePath)
	for _, dir := range debugStmtExemptDirs {
		if strings.Contains(lowerPath, dir) {
			return ""
		}
	}

	// Cap scan length
	scanNew := newContent
	if len(scanNew) > maxDebugScanLen {
		scanNew = scanNew[:maxDebugScanLen]
	}
	scanOld := oldContent
	if len(scanOld) > maxDebugScanLen {
		scanOld = scanOld[:maxDebugScanLen]
	}

	ext := strings.ToLower(filepath.Ext(filePath))

	var flagged []string

	for _, dp := range leftoverDebugPatterns {
		// Restrict to matching extensions if specified
		if dp.exts != nil && !dp.exts[ext] {
			continue
		}

		oldMatches := dp.pattern.FindAllString(scanOld, -1)
		newMatches := dp.pattern.FindAllString(scanNew, -1)

		introduced := len(newMatches) - len(oldMatches)
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

// formatDebugStmtWarning renders a concise warning for newly-introduced debug statements.
func formatDebugStmtWarning(flagged []string) string {
	return fmt.Sprintf(
		"[DEBUG STATEMENT WARNING] Detected new debug print statement(s): %s. "+
			"These are typically added during debugging and forgotten. "+
			"Remove them before completing the task, or use a proper logging framework "+
			"if the output is intentional.",
		strings.Join(flagged, ", "))
}
