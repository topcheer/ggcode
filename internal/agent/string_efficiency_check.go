package agent

// Post-Edit String Handling & Concatenation Intelligence
//
// Trend: String handling efficiency is a core Go optimization direction.
// Representive tools: gocritic (strConcat, appendConcat, equalFold),
// staticcheck (SA1024 slice/string conversion), go vet (printf).
//
// This check covers two distinct anti-patterns NOT caught by loop_perf_check.go:
//
//  1. Case-insensitive string comparison via double ToLower/ToUpper:
//     strings.ToLower(a) == strings.ToLower(b)
//     Each ToLower/ToUpper allocates a new string, performing two heap
//     allocations and two full scans. strings.EqualFold(a, b) does zero
//     allocations and short-circuits on the first mismatched rune.
//     Benchmark: EqualFold is ~5-10x faster and allocates 0 bytes vs 2.
//
//  2. fmt.Sprint/fmt.Sprintln used for concatenating only string operands:
//     fmt.Sprint("prefix: ", value) where value is a string.
//     fmt.Sprint uses reflection (interface boxing) even for two strings.
//     Direct concatenation (a + b) is ~3x faster and allocates less.
//     gocritic flags this as "strConcat" / staticcheck flags S1021.
//
// Competitor analysis:
//   - gocritic: detects both patterns (equalFold, strConcat)
//   - staticcheck: detects fmt.Sprint string concat (S1021)
//   - go vet: does NOT detect either
//   - Claude Code / Cursor / Cline: no inline detection at write time
//   - CodeRabbit: may flag in PR review but not at write time
//
// This check provides INLINE detection with zero LLM cost. It is delta-aware
// (only flags patterns newly introduced by this edit).

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// stringEffIssue records a single string efficiency anti-pattern.
type stringEffIssue struct {
	pattern string // "equalfold" or "fmt.Sprint concat"
	line    int    // 1-based line number
}

// checkStringEfficiency detects string handling anti-patterns in Go source.
// Returns warning strings. Only flags NEW occurrences (delta-aware).
func checkStringEfficiency(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}
	if isTestFile(filePath) {
		return nil
	}

	newIssues := findStringEffIssues(filePath, newContent)
	if len(newIssues) == 0 {
		return nil
	}

	// Delta-aware: subtract pre-existing issues.
	if strings.TrimSpace(oldContent) != "" {
		oldIssues := findStringEffIssues(filePath, oldContent)
		if len(oldIssues) > 0 {
			oldCount := make(map[string]int)
			for _, iss := range oldIssues {
				oldCount[iss.pattern]++
			}
			filtered := newIssues[:0]
			newCount := make(map[string]int)
			for _, iss := range newIssues {
				newCount[iss.pattern]++
				if newCount[iss.pattern] <= oldCount[iss.pattern] {
					continue
				}
				filtered = append(filtered, iss)
			}
			newIssues = filtered
		}
	}

	if len(newIssues) == 0 {
		return nil
	}

	patternCounts := make(map[string]int)
	for _, iss := range newIssues {
		patternCounts[iss.pattern]++
	}

	summary := buildStringEffSummary(patternCounts)
	msg := buildStringEffMessage(summary, patternCounts)

	return []string{msg}
}

// buildStringEffSummary creates a comma-separated summary of detected patterns.
func buildStringEffSummary(patternCounts map[string]int) string {
	var parts []string
	for pat, count := range patternCounts {
		entry := pat
		if count > 1 {
			entry = fmt.Sprintf("%s (%d occurrences)", pat, count)
		}
		parts = append(parts, entry)
	}
	return joinStrings(parts, ", ")
}

// buildStringEffMessage constructs the warning message based on which patterns were found.
func buildStringEffMessage(summary string, patternCounts map[string]int) string {
	_, hasEqualFold := patternCounts["equalfold"]
	_, hasSprint := patternCounts["fmt.Sprint concat"]

	switch {
	case hasEqualFold && hasSprint:
		return "Introduced string efficiency anti-pattern(s): " + summary +
			". Use strings.EqualFold(a, b) instead of ToLower(a)==ToLower(b)" +
			" (saves 2 allocations, short-circuits on first mismatch)." +
			" For fmt.Sprint with only string operands, use direct concatenation (a + b)" +
			" to avoid reflection and interface boxing overhead."
	case hasEqualFold:
		return "Introduced string efficiency anti-pattern(s): " + summary +
			". strings.ToLower(a)==strings.ToLower(b) allocates two new strings" +
			" and performs two full scans. Use strings.EqualFold(a, b) which" +
			" allocates nothing and short-circuits on the first mismatched rune" +
			" (~5-10x faster)."
	default:
		return "Introduced string efficiency anti-pattern(s): " + summary +
			". fmt.Sprint used with only string operands incurs reflection" +
			" and interface boxing overhead. Use direct concatenation (a + b)" +
			" or strings.Join for 3+ parts (~3x faster, less allocation)."
	}
}

// anti-patterns found.
func findStringEffIssues(filename, src string) []stringEffIssue {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil || file == nil {
		return nil
	}

	stringVars := identifyStringVars(file)
	collectStringParams(file, stringVars)

	var results []stringEffIssue

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.BinaryExpr:
			// Check: strings.ToLower(x) == strings.ToLower(y)
			// or:    strings.ToUpper(x) == strings.ToUpper(y)
			if node.Op == token.EQL || node.Op == token.NEQ {
				if isCaseConvertCall(node.X) && isCaseConvertCall(node.Y) {
					pos := fset.Position(node.Pos())
					results = append(results, stringEffIssue{
						pattern: "equalfold",
						line:    pos.Line,
					})
					return true
				}
			}

		case *ast.CallExpr:
			name := qualifiedCallName(node)
			if name == "fmt.Sprint" {
				// Check if ALL arguments are known string values.
				// For only-string operands, direct concatenation (a + b)
				// avoids reflection and interface boxing.
				if allStringOperands(node.Args, stringVars) && len(node.Args) >= 2 {
					pos := fset.Position(node.Pos())
					results = append(results, stringEffIssue{
						pattern: "fmt.Sprint concat",
						line:    pos.Line,
					})
				}
			}
		}
		return true
	})

	return results
}

// isCaseConvertCall returns true if the expression is strings.ToLower(...) or
// strings.ToUpper(...).
func isCaseConvertCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	name := qualifiedCallName(call)
	return name == "strings.ToLower" || name == "strings.ToUpper"
}

// collectStringParams scans function declarations for string-typed parameters
// and adds them to the stringVars set.
func collectStringParams(file *ast.File, stringVars map[string]bool) {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fn.Type != nil {
			collectStringFields(fn.Type.Params, stringVars)
			collectStringFields(fn.Type.Results, stringVars)
		}
	}
}

// collectStringFields marks names from a FieldList as string-typed if their
// type annotation is "string".
func collectStringFields(fl *ast.FieldList, stringVars map[string]bool) {
	if fl == nil {
		return
	}
	for _, field := range fl.List {
		if !isStringTypeAnnotation(field.Type) {
			continue
		}
		for _, name := range field.Names {
			stringVars[name.Name] = true
		}
	}
}

// allStringOperands returns true if every expression in args is a known string
// value (string literal, string-typed identifier, or string-returning call).
func allStringOperands(args []ast.Expr, stringVars map[string]bool) bool {
	if len(args) == 0 {
		return false
	}
	for _, arg := range args {
		if isStringExpr(arg) {
			continue
		}
		// Check if it's a known string identifier.
		if ident, ok := arg.(*ast.Ident); ok && stringVars[ident.Name] {
			continue
		}
		return false
	}
	return true
}
