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

// debugLineComment matches // line comments (JS/TS/Go/Rust/PHP/Swift) and
// # line comments (Python/Ruby). Used to blank comment text before pattern
// matching so a mention inside a comment — e.g. the very common
// `// removed fmt.Println("dbg") below` — is not counted as a debug
// statement (#544 Bug A, same class as #527's deprecated_api fix).
var debugLineComment = regexp.MustCompile(`(//|#)[^\n]*`)

// debugBlockCommentStart matches /* ... */ block comments (JS/TS/PHP/Swift).
// Go also has them; a simple non-greedy across-the-whole-text regex is used —
// it is the same trade-off deprecated_api_check made for comment awareness.
var debugBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)

// stripDebugComments blanks line and block comments while preserving
// newlines, so per-line structure and offsets stay stable. String literals
// containing // or # (e.g. "https://x") may be over-stripped from the line's
// tail — acceptable here: the check is delta-based (old vs new), so both
// sides lose the same tails and no false introduction is manufactured.
func stripDebugComments(content string) string {
	out := debugBlockComment.ReplaceAllStringFunc(content, func(m string) string {
		return strings.Repeat("\n", strings.Count(m, "\n"))
	})
	out = debugLineComment.ReplaceAllStringFunc(out, func(m string) string {
		return strings.Repeat("\n", strings.Count(m, "\n"))
	})
	return out
}

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
//
// Design rule (#512): only flag UNAMBIGUOUS debug signals. Language-standard
// stdout primitives are deliberately NOT matched — print() is the only way
// to write output in Python, printf in C, System.out in Java, print in
// Swift/Dart, println! in Rust, and puts/p in Ruby; CLI programs' normal
// output is token-indistinguishable from debug prints, and coaching the agent
// to "remove them" trains it to delete legitimate output (sa-171 probes:
// a 10-print Python CLI reported "10 x print() (Python)"). Languages are
// only covered when (a) an explicit debug construct exists (debugger;,
// breakpoint(), dbg!, pprint, var_dump, debugPrint/dump), or (b) the language
// has a strong logging convention making bare prints a leftover signal in
// agent-written non-test code (Go's log.Printf, JS's structured loggers).
var leftoverDebugPatterns = []leftoverDebugPattern{
	// --- Go ---
	// Kept: Go's strong log convention makes bare fmt.Print in non-test
	// agent-written code a leftover-debug signal (sa-171 A5: true positive).
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
	// print() removed (#512): Python's only stdout primitive. Only the
	// explicit debug constructs remain.
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
	// println!/print!/eprintln!/eprint removed (#512): Rust's standard
	// output macros. Only dbg! is an explicit debug signal.
	{
		name:    "dbg! macro (Rust)",
		pattern: regexp.MustCompile(`\bdbg!`),
		exts:    map[string]bool{".rs": true},
	},

	// Ruby group removed (#512): puts/p/pp/warn are Ruby's standard output
	// and warning primitives; no unambiguous debug construct exists, and the
	// regex had no comment awareness (`# p(x)` warned, sa-171 A6).

	// Java/Kotlin group removed (#512): System.out is the standard output
	// primitive (sa-171 A3 FP).

	// C/C++ group removed (#512): printf/fprintf/puts/cout are the standard
	// output primitives (sa-171 A2 FP).

	// --- PHP ---
	{
		name:    "var_dump/print_r (PHP)",
		pattern: regexp.MustCompile(`\b(var_dump|print_r|dump)\s*\(`),
		exts:    map[string]bool{".php": true},
	},

	// Dart group removed (#512): print is the standard output primitive
	// (sa-171 A4 FP); no unambiguous debug construct.

	// --- Swift ---
	// print removed (#512, sa-171 A4 FP); debugPrint/dump remain explicit
	// debug signals.
	{
		name:    "debugPrint/dump (Swift)",
		pattern: regexp.MustCompile(`\b(debugPrint|dump)\s*\(`),
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

	// #544 Bug A: blank comments BEFORE matching. A comment that merely
	// MENTIONS a debug call — `// removed fmt.Println("dbg") below` — must
	// not be counted as introducing one. Same comment-awareness class as
	// #527's deprecated_api fix; applied to both sides of the delta so the
	// set comparison stays symmetric.
	scanNew = stripDebugComments(scanNew)
	scanOld = stripDebugComments(scanOld)

	ext := strings.ToLower(filepath.Ext(filePath))

	var flagged []string

	for _, dp := range leftoverDebugPatterns {
		// Restrict to matching extensions if specified
		if dp.exts != nil && !dp.exts[ext] {
			continue
		}

		oldMatches := dp.pattern.FindAllString(scanOld, -1)
		newMatches := dp.pattern.FindAllString(scanNew, -1)

		// Per-instance set comparison (fix #171 — count-diff is blind to
		// remove-N-add-N; see hardcoded_secret_check.go).
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

// formatDebugStmtWarning renders a concise warning for newly-introduced debug statements.
func formatDebugStmtWarning(flagged []string) string {
	return fmt.Sprintf(
		"[DEBUG STATEMENT WARNING] Detected new debug print statement(s): %s. "+
			"These are typically added during debugging and forgotten. "+
			"Remove them before completing the task, or use a proper logging framework "+
			"if the output is intentional.",
		strings.Join(flagged, ", "))
}
