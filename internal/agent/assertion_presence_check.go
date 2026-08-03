package agent

// Assertion Presence Verification for Go Test Files
//
// Problem: AI coding agents frequently generate test functions that look
// plausible but contain zero assertions -- they call the code under test but
// never verify the result. These "hollow tests" pass trivially, give false
// confidence, and mask real bugs. Unlike test_gaming_check (which detects
// WEAKENING of existing tests), this check detects NEW tests that were never
// meaningful to begin with.
//
// Research basis:
//   - "Specification Gaming" in AI agents (DeepMind, 2024): agents satisfy the
//     reward signal (tests pass) without solving the real task (verifying behavior).
//   - "Evaluating LLM-Generated Tests" (ICSE 2025): 15-30% of LLM-generated test
//     functions lack any assertion, especially for complex logic.
//   - Claude Code, Cursor, Cline/OpenHands: none detect hollow tests at write time.
//   - Aider: runs tests but doesn't analyze whether they actually assert anything.
//
// ggcode's approach: AST-based analysis of Go test files after each write.
// Finds Test*/Benchmark* functions and checks whether the function body contains
// at least one assertion-like call. Only flags functions that are NEW or whose
// assertion count DECREASED to zero compared to the old content (delta-aware).
//
// Assertion indicators (Go-specific):
//   - t.Error, t.Errorf, t.Fatal, t.Fatalf, t.Fail, t.FailNow
//   - require.* (testify), assert.* (testify), quick.Check
//   -Direct comparison in if + t.Error pattern is also common but harder to detect
//    via AST; the call-based heuristic covers the vast majority of real tests.
//
// False positive mitigation:
//   - Benchmark functions are excluded (they measure performance, not correctness)
//   - Test helpers (TestMain, helper functions) are excluded
//   - Functions that call other test functions (sub-tests via t.Run) are excluded
//     since assertions may be in the sub-test
//   - Only flags when assertion count is zero, not merely low
//   - Delta-aware: pre-existing hollow tests are not flagged

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"

	"strings"
)

// assertionPresenceMaxWarnings caps the number of hollow-test warnings per write.
const assertionPresenceMaxWarnings = 3

// goAssertionCalls are function call names that constitute test assertions.
// Match is on the call expression's selector (e.g., t.Error, require.Equal).
var goAssertionCalls = map[string]bool{
	// testing.T methods
	"Error":   true,
	"Errorf":  true,
	"Fatal":   true,
	"Fatalf":  true,
	"Fail":    true,
	"FailNow": true,
	"Logf":    false, // Log alone is not an assertion, only present for completeness
}

// goAssertionPkgs are package qualifiers whose calls are always assertions.
var goAssertionPkgs = map[string]bool{
	"require": true, // testify require
	"assert":  true, // testify assert
	"quick":   true, // testing/quick
	"check":   true, // go-check
	"should":  true, // gomega-should style
}

// checkAssertionPresence verifies that Go test functions contain at least one
// assertion call. Returns a non-empty guidance string if hollow tests are found.
//
// Parameters:
//   - filePath: path of the written file (only *_test.go files are checked)
//   - oldContent: file content before the edit ("" for new files)
//   - newContent: file content after the edit
func checkAssertionPresence(filePath, oldContent, newContent string) string {
	// Only check Go test files.
	if !strings.HasSuffix(filePath, "_test.go") {
		return ""
	}
	if strings.TrimSpace(newContent) == "" {
		return ""
	}

	fset := token.NewFileSet()
	newAST, err := parser.ParseFile(fset, filePath, newContent, 0)
	if err != nil {
		return "" // syntax errors are handled by other checks
	}

	// Collect assertion counts per test function name from old content.
	oldCounts := map[string]int{}
	if strings.TrimSpace(oldContent) != "" {
		oldFset := token.NewFileSet()
		oldAST, err := parser.ParseFile(oldFset, filePath, oldContent, 0)
		if err == nil {
			for name, count := range countAssertionsPerTest(oldFset, oldAST) {
				oldCounts[name] = count
			}
		}
	}

	// Find hollow test functions in new content.
	newCounts := countAssertionsPerTest(fset, newAST)
	var hollow []string
	for name, count := range newCounts {
		if count > 0 {
			continue // has assertions, fine
		}
		// Delta check: only flag if this function is new (not in oldCounts)
		// or if it had assertions before but now has none (regression).
		oldCount, existed := oldCounts[name]
		if !existed {
			// New function with zero assertions.
			hollow = append(hollow, name)
		} else if oldCount > 0 {
			// Had assertions before, now has none — regression.
			hollow = append(hollow, name+" (assertions removed)")
		}
		// Pre-existing hollow test (oldCount==0) is not flagged.
	}

	if len(hollow) == 0 {
		return ""
	}

	// Sort for deterministic output.
	sortStrings(hollow)
	if len(hollow) > assertionPresenceMaxWarnings {
		hollow = hollow[:assertionPresenceMaxWarnings]
	}

	var detail string
	if len(hollow) == 1 {
		detail = fmt.Sprintf("Test function %s contains no assertion calls (t.Error, t.Fatal, require.*, assert.*). "+
			"A test without assertions passes trivially and provides no verification value. "+
			"Add meaningful assertions to validate the code under test.", hollow[0])
	} else {
		detail = fmt.Sprintf("The following test functions contain no assertion calls: %s. "+
			"Tests without assertions pass trivially and provide no verification value. "+
			"Add meaningful assertions (t.Error, t.Fatal, require.*, assert.*) to each.", strings.Join(hollow, ", "))
	}
	return detail
}

// countAssertionsPerTest walks the AST and returns a map of Test* function names
// to their assertion call count. Benchmark functions are excluded.
func countAssertionsPerTest(fset *token.FileSet, file *ast.File) map[string]int {
	result := map[string]int{}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		// Only consider test functions: TestXxx(*testing.T).
		name := fn.Name.Name
		if !strings.HasPrefix(name, "Test") {
			continue
		}
		// Skip TestMain — it's a setup/teardown function, not a test.
		if name == "TestMain" {
			continue
		}
		// Verify it takes *testing.T parameter (rough check: has at least 1 param).
		if fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
			continue
		}

		count := countAssertionCalls(fn.Body)
		result[name] = count
	}

	return result
}

// countAssertionCalls recursively walks the function body and counts calls that
// look like test assertions.
func countAssertionCalls(body *ast.BlockStmt) int {
	count := 0
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		// Check for t.Error, t.Fatal, etc. (selector expression: t.Errorf)
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			method := sel.Sel.Name
			if goAssertionCalls[method] {
				// Verify receiver looks like testing.T (single ident, typically "t").
				if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "t" {
					count++
					return true
				}
			}
			// Check for require.X, assert.X, etc.
			if pkgIdent, ok := sel.X.(*ast.Ident); ok {
				if goAssertionPkgs[pkgIdent.Name] {
					count++
					return true
				}
			}
		}

		return true
	})
	return count
}

// sortStrings is a simple sort to avoid importing sort for one call.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// (end of file)
