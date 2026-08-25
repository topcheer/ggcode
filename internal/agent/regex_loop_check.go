package agent

// Regex Compile-in-Loop Detection
//
// Trend: AI Agent Performance Awareness — Expensive Operation in Loop
//
// Problem: AI coding agents frequently generate Go code that compiles regex
// patterns inside for/range loops using regexp.Compile, regexp.MustCompile,
// regexp.CompilePOSIX, or regexp.MustCompilePOSIX. Each call re-parses and
// compiles the pattern from scratch — an O(m) operation where m is the pattern
// length — even when the pattern string is identical across iterations.
//
//   for _, input := range inputs {
//       re := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`) // compiled N times!
//       if re.MatchString(input) { ... }
//   }
//
// For N iterations, this wastes O(N*m) CPU and allocates N Regexp objects
// that are immediately garbage-collected. Benchmark: compiling a typical
// regex takes ~5-20 microseconds; for N=10,000 iterations that's 50-200ms
// of wasted CPU.
//
// Correct pattern: compile once at package level and reuse.
//
//   var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
//
//   for _, input := range inputs {
//       if dateRe.MatchString(input) { ... }
//   }
//
// Competitor analysis:
//   - Claude Code: no detection (relies on external profilers)
//   - Cursor: lint-on-save does not flag regex compile in loops
//   - Cline/OpenHands: reactive only — caught by profiling
//   - Aider: no detection
//   - go vet: does not flag regex compile in loops
//   - staticcheck: does not flag regex compile in loops
//   - gocritic: no equivalent check
//
// This check provides immediate, zero-dependency feedback at write time.
// It is distinct from loop_perf_check (string concat) and nplus1_loop_check
// (I/O in loops) — regex compilation is a CPU-bound cost, not I/O or
// allocation-pattern.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"
)

// regexCompileFuncs lists regexp package functions that compile patterns.
var regexCompileFuncs = map[string]bool{
	"regexp.Compile":          true,
	"regexp.MustCompile":      true,
	"regexp.CompilePOSIX":     true,
	"regexp.MustCompilePOSIX": true,
}

// regexLoopIssue records a single regex-compile-in-loop occurrence.
type regexLoopIssue struct {
	funcName string // e.g., "regexp.MustCompile"
	pattern  string // source text of the pattern argument (fingerprint)
	pos      token.Pos
}

// fingerprint identifies a compile-in-loop instance by function + pattern
// expression, independent of position (positions shift across edits, so the
// delta comparison must be instance-based, not count-based #1017).
func (i regexLoopIssue) fingerprint() string {
	return i.funcName + "|" + i.pattern
}

// checkRegexLoop detects regexp.Compile/MustCompile/CompilePOSIX/MustCompilePOSIX
// calls inside for/range loops in Go code. Returns warning strings.
// Delta-aware: only flags patterns newly introduced by this edit.
func checkRegexLoop(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	newIssues := findRegexInLoops(filePath, newContent)
	if len(newIssues) == 0 {
		return nil
	}

	// Delta: subtract patterns already present in old content by fingerprint
	// set difference, NOT count comparison. Count comparison both under-
	// reports (replacing OLD1/OLD2 with NEW1/NEW2 keeps counts equal, so newly
	// introduced patterns pass silently) and over-reports (an added instance
	// makes untouched old instances count as "new") (#1017).
	if strings.TrimSpace(oldContent) != "" {
		oldFPs := make(map[string]bool)
		for _, oi := range findRegexInLoops(filePath, oldContent) {
			oldFPs[oi.fingerprint()] = true
		}
		if len(oldFPs) > 0 {
			fresh := newIssues[:0]
			for _, ni := range newIssues {
				if !oldFPs[ni.fingerprint()] {
					fresh = append(fresh, ni)
				}
			}
			newIssues = fresh
			if len(newIssues) == 0 {
				return nil
			}
		}
	}

	fset := token.NewFileSet()
	_, err := parser.ParseFile(fset, filePath, newContent, 0)
	if err != nil {
		return []string{fmt.Sprintf(
			"Detected %d regexp.Compile/MustCompile call(s) inside a loop in %s. "+
				"Regex patterns are recompiled on every iteration (O(m) each). "+
				"Move compilation to package level: `var re = regexp.MustCompile(pattern)`.",
			len(newIssues), filepath.Base(filePath))}
	}

	var warnings []string
	for i, issue := range newIssues {
		if i >= 2 {
			break
		}
		warnings = append(warnings, fmt.Sprintf(
			"%s call inside loop at %s:%d. The regex is recompiled on every "+
				"iteration. Move compilation outside the loop (e.g., to a package-level "+
				"var) and reuse the compiled *regexp.Regexp.",
			issue.funcName, filepath.Base(filePath), fset.Position(issue.pos).Line))
	}
	if extra := len(newIssues) - 2; extra > 0 {
		warnings = append(warnings,
			fmt.Sprintf("...and %d more regex-compile-in-loop pattern(s) in %s",
				extra, filepath.Base(filePath)))
	}
	return warnings
}

// findRegexInLoops parses Go source and returns all regex-compile-in-loop
// occurrences. Scans for/range loop bodies (recursively through nested
// if/switch/select blocks) but skips nested function literals.
func findRegexInLoops(filename, src string) []regexLoopIssue {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil
	}

	var results []regexLoopIssue

	// The outer Inspect visits nested loop statements after already scanning
	// them as part of the enclosing loop body, so the same call is reached
	// twice with the same position — dedup by pos (#1017).
	seenPos := make(map[token.Pos]bool)

	ast.Inspect(file, func(n ast.Node) bool {
		var body *ast.BlockStmt
		switch node := n.(type) {
		case *ast.ForStmt:
			body = node.Body
		case *ast.RangeStmt:
			body = node.Body
		default:
			return true
		}
		if body == nil {
			return true
		}

		ast.Inspect(body, func(inner ast.Node) bool {
			if _, isFuncLit := inner.(*ast.FuncLit); isFuncLit {
				return false
			}
			call, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := callFuncName(call)
			if regexCompileFuncs[name] {
				if seenPos[call.Pos()] {
					return true
				}
				seenPos[call.Pos()] = true
				pattern := ""
				if len(call.Args) > 0 {
					pattern = types.ExprString(call.Args[0])
				}
				results = append(results, regexLoopIssue{
					funcName: name,
					pattern:  pattern,
					pos:      call.Pos(),
				})
			}
			return true
		})
		return true
	})

	return results
}
