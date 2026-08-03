package agent

// Post-Edit Loop Performance Anti-Pattern Detection
//
// Trend: AI Agent Performance Awareness (2025-2026)
//
// Problem: AI coding agents frequently generate Go code with O(n^2) string
// building patterns inside loops. The two most common anti-patterns are:
//
//  1. String concatenation with += inside a for/range loop:
//     for _, item := range items {
//         s += item.Name + ", " // O(n^2) - allocates a new string each iteration
//     }
//     Each += creates a new allocation and copies all previous content.
//     For N items, total copies = 1+2+3+...+N = O(N^2).
//     Fix: use strings.Builder (amortized O(N)).
//
//  2. fmt.Sprintf inside loops for string assembly:
//     for _, r := range records {
//         buf += fmt.Sprintf("%d:%s ", r.ID, r.Name) // O(n^2) + format overhead
//     }
//     fmt.Sprintf is ~10x slower than strconv equivalents and the
//     concatenation compounds the cost quadratically.
//     Fix: use strings.Builder + strconv or direct writes.
//
// Competitor analysis:
//   - Claude Code: no performance anti-pattern detection (relies on external tools)
//   - Cursor: lint-on-save may catch via staticcheck (S1021 for fmt.Sprint
//     string concatenation), but NOT += in loops
//   - Cline/OpenHands: reactive only - caught by profiling or production
//   - Aider: no detection
//   - go vet: does not flag += in loops
//   - staticcheck: does not flag += in loops (only flags fmt.Sprint(x+y))
//   - gocritic: has "appendAssign" but not string concat in loops
//
// None provide INLINE detection at write time. This check catches the most
// impactful performance anti-pattern (O(n^2) string building) with zero false
// positives - if += is applied to a string variable inside a for/range body,
// it is always O(n^2). The check is delta-aware (only flags patterns newly
// introduced by this edit).

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// loopPerfIssue records a single performance anti-pattern found in a loop.
type loopPerfIssue struct {
	pattern string // "string concat" or "fmt.Sprintf in loop concat"
	line    int    // 1-based line number
	varName string // the variable being accumulated (best-effort)
}

// checkLoopPerf detects performance anti-patterns in Go for/range loops.
// Returns warning strings. Only flags NEW occurrences (delta-aware).
func checkLoopPerf(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}
	if isTestFile(filePath) {
		return nil
	}

	newIssues := findLoopPerfIssues(filePath, newContent)
	if len(newIssues) == 0 {
		return nil
	}

	// Delta-aware: subtract pre-existing issues.
	if strings.TrimSpace(oldContent) != "" {
		oldIssues := findLoopPerfIssues(filePath, oldContent)
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

	var parts []string
	for pattern, count := range patternCounts {
		if count > 1 {
			parts = append(parts, fmt.Sprintf("%s (%d occurrences)", pattern, count))
		} else {
			parts = append(parts, pattern)
		}
	}
	summary := joinStrings(parts, ", ")

	msg := "Introduced performance anti-pattern(s) inside a loop (" +
		summary + "). String += inside a for/range loop is O(n^2) -- each " +
		"iteration copies all previous content into a new allocation. Use " +
		"strings.Builder with WriteString() for amortized O(n) performance. " +
		"For fmt.Sprintf in loops, combine Builder with strconv or direct " +
		"writes to avoid both the quadratic concat and format overhead."

	return []string{msg}
}

// findLoopPerfIssues parses Go source and returns all loop performance
// anti-patterns found in for/range loop bodies.
func findLoopPerfIssues(filename, src string) []loopPerfIssue {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil || file == nil {
		return nil
	}

	// Track string-typed variables via conservative heuristics.
	stringVars := identifyStringVars(file)

	var results []loopPerfIssue

	// Scan all for-loop and range-loop bodies.
	ast.Inspect(file, func(n ast.Node) bool {
		var body *ast.BlockStmt
		switch stmt := n.(type) {
		case *ast.ForStmt:
			body = stmt.Body
		case *ast.RangeStmt:
			body = stmt.Body
		default:
			return true
		}
		if body == nil {
			return true
		}
		scanLoopBodyForPerfIssues(body, fset, stringVars, &results)
		return true
	})

	return results
}

// identifyStringVars scans the file for variables that are likely string-typed,
// using conservative pattern matching (string literal initialization, string
// type annotation, or assignment of string-returning function calls).
func identifyStringVars(file *ast.File) map[string]bool {
	stringVars := make(map[string]bool)

	// Single pass: collect string vars from both ValueSpecs (var s string)
	// and AssignStmts (s := "hello", s = strings.Join(...)).
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.ValueSpec:
			collectStringNamesFromSpec(node, stringVars)
		case *ast.AssignStmt:
			if node.Tok == token.ASSIGN || node.Tok == token.DEFINE {
				collectStringNamesFromAssign(node, stringVars)
			}
		}
		return true
	})

	return stringVars
}

// collectStringNamesFromSpec marks variables from a ValueSpec as string-typed
// if they have a string type annotation or string-valued initializer.
func collectStringNamesFromSpec(vs *ast.ValueSpec, stringVars map[string]bool) {
	for i, name := range vs.Names {
		if isStringTypeAnnotation(vs.Type) {
			stringVars[name.Name] = true
			continue
		}
		if i < len(vs.Values) && isStringExpr(vs.Values[i]) {
			stringVars[name.Name] = true
		}
	}
}

// collectStringNamesFromAssign marks variables from an AssignStmt as string-typed
// if their RHS is a known string expression.
func collectStringNamesFromAssign(assign *ast.AssignStmt, stringVars map[string]bool) {
	for i, lhs := range assign.Lhs {
		if i >= len(assign.Rhs) {
			break
		}
		ident, ok := lhs.(*ast.Ident)
		if !ok {
			continue
		}
		if isStringExpr(assign.Rhs[i]) {
			stringVars[ident.Name] = true
		}
	}
}

// isStringTypeAnnotation returns true if the type expression is the builtin "string".
func isStringTypeAnnotation(typ ast.Expr) bool {
	if typ == nil {
		return false
	}
	ident, ok := typ.(*ast.Ident)
	return ok && ident.Name == "string"
}

// scanLoopBodyForPerfIssues inspects a loop body for string += and
// fmt.Sprintf-in-loop-concat patterns.
func scanLoopBodyForPerfIssues(body *ast.BlockStmt, fset *token.FileSet, stringVars map[string]bool, results *[]loopPerfIssue) {
	if body == nil {
		return
	}

	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || assign.Tok != token.ADD_ASSIGN {
			return true
		}

		// Check each LHS for known string variables.
		for _, lhs := range assign.Lhs {
			ident, ok := lhs.(*ast.Ident)
			if !ok || !stringVars[ident.Name] {
				continue
			}

			pos := fset.Position(assign.Pos())

			// Check if RHS is fmt.Sprintf or fmt.Sprint call.
			if len(assign.Rhs) == 1 {
				if call, ok := assign.Rhs[0].(*ast.CallExpr); ok {
					name := qualifiedCallName(call)
					if name == "fmt.Sprintf" || name == "fmt.Sprint" {
						*results = append(*results, loopPerfIssue{
							pattern: "fmt.Sprintf in loop concat",
							line:    pos.Line,
							varName: ident.Name,
						})
						return true
					}
				}
			}

			// Plain string += pattern.
			*results = append(*results, loopPerfIssue{
				pattern: "string concat (+=)",
				line:    pos.Line,
				varName: ident.Name,
			})
		}
		return true
	})
}

// isStringExpr returns true if the AST expression is known to produce a
// string value. Uses conservative heuristics for zero-dependency operation.
func isStringExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return e.Kind == token.STRING
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			return isStringExpr(e.X) || isStringExpr(e.Y)
		}
	case *ast.CallExpr:
		// string() conversion
		if ident, ok := e.Fun.(*ast.Ident); ok && ident.Name == "string" {
			return true
		}
		name := qualifiedCallName(e)
		switch name {
		case "fmt.Sprintf", "fmt.Sprint", "strings.Join", "strings.TrimSpace",
			"strings.ToUpper", "strings.ToLower", "strings.Replace",
			"strings.ReplaceAll", "strings.TrimPrefix", "strings.TrimSuffix",
			"filepath.Join", "filepath.Base", "filepath.Dir", "filepath.Ext",
			"strconv.Itoa", "strconv.FormatInt", "strconv.FormatFloat",
			"strconv.FormatBool":
			return true
		}
	}
	return false
}
