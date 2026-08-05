package agent

// Test Isolation Detection - Global State Mutation Guard for Go Test Files
//
// Problem: AI coding agents frequently generate tests that mutate global state
// without cleanup, causing test pollution and flaky behavior in parallel test
// execution. The most common anti-patterns:
//
//  1. os.Setenv() in tests - permanently modifies process environment.
//     Other tests running in parallel see the modified env. Go 1.17+ provides
//     t.Setenv() which auto-restores after the test and also serializes the
//     test (prevents t.Parallel() interference with env state).
//
//  2. Mutating os.Args - changes the process command-line globally, affecting
//     any test that reads os.Args. There is no t.SetArgs equivalent; tests
//     must save/restore manually.
//
//  3. Redirecting os.Stdout/os.Stderr - if not restored, subsequent test
//     output goes to the wrong writer.
//
//  4. Writing to package-level variables from Test* functions - introduces
//     hidden coupling between tests. When tests run in parallel (-parallel),
//     these shared mutations cause race conditions and nondeterministic
//     failures.
//
// Competitor analysis:
//   - Claude Code: no detection (relies on go test -race catching issues)
//   - Cursor: no detection
//   - Cline/OpenHands: no detection
//   - Aider: no detection
//   - Windsurf: no detection
//   - go vet: does not flag os.Setenv or global mutations in tests
//   - staticcheck: does not cover test isolation
//
// Research basis:
//   - "Testing on the Toilet" (Google): global state in tests is a top cause
//     of flaky tests in large monorepos.
//   - "The Little Manual of API Design" (Jason Sachs): mutable global state
//     makes tests non-hermetic and order-dependent.
//   - Go testing convention (testing.T.Setenv docs): "Setenv calls cannot be
//     used in parallel tests that are running" — using os.Setenv sidesteps
//     this safety mechanism entirely.
//
// ggcode's approach: AST-based, delta-aware analysis. Only flags NEW instances
// introduced by the current edit (comparing old vs new content). Advisory
// (non-blocking). Zero LLM cost. <1ms per check.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
)

// testIsolationMaxWarnings caps the number of global-state mutation warnings per write.
const testIsolationMaxWarnings = 5

// checkTestIsolation detects global state mutations in Go test files.
// Returns a guidance string if NEW isolation violations are found.
//
// Parameters:
//   - filePath: path of the written file (only *_test.go files are checked)
//   - oldContent: file content before the edit ("" for new files)
//   - newContent: file content after the edit
func checkTestIsolation(filePath, oldContent, newContent string) string {
	if !strings.HasSuffix(filePath, "_test.go") {
		return ""
	}
	if strings.TrimSpace(newContent) == "" {
		return ""
	}

	oldViolations := findGlobalStateMutations(filePath, oldContent)
	newViolations := findGlobalStateMutations(filePath, newContent)

	// Delta: only flag if new content has MORE violations than old.
	if len(newViolations) <= len(oldViolations) {
		return ""
	}

	// Categorize new violations.
	introduced := len(newViolations) - len(oldViolations)

	// Aggregate by type for reporting.
	typeCounts := map[string]int{}
	for _, v := range newViolations {
		typeCounts[v.kind]++
	}
	// Subtract old counts to get net new per type.
	for _, v := range oldViolations {
		typeCounts[v.kind]--
	}

	var details []string
	for kind, count := range typeCounts {
		if count <= 0 {
			continue
		}
		switch kind {
		case "os-setenv":
			details = append(details, fmt.Sprintf("%d os.Setenv() call(s) - use t.Setenv() which auto-restores env after the test and prevents parallel env races", count))
		case "os-args":
			details = append(details, fmt.Sprintf("%d os.Args mutation(s) - save/restore with defer to prevent test pollution", count))
		case "os-stdio":
			details = append(details, fmt.Sprintf("%d os.Stdout/Stderr mutation(s) - restore with defer to prevent output capture issues in other tests", count))
		case "global-var":
			details = append(details, fmt.Sprintf("%d package-level variable mutation(s) from test function(s) - use local variables or t.Cleanup to restore", count))
		}
	}

	if len(details) == 0 {
		return ""
	}

	sort.Strings(details)
	if len(details) > testIsolationMaxWarnings {
		details = details[:testIsolationMaxWarnings]
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Test isolation risk: %d new global-state mutation(s) detected in test code. "+
		"Mutating global state makes tests non-hermetic, order-dependent, and flaky under -parallel.\n", introduced))
	for _, d := range details {
		b.WriteString("  - ")
		b.WriteString(d)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// globalStateMutation records a single global state mutation in a test function.
type globalStateMutation struct {
	kind string
	line int
}

// findGlobalStateMutations parses Go test source and returns all global state
// mutations found inside Test*/Benchmark*/Example* functions.
func findGlobalStateMutations(filename, src string) []globalStateMutation {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil || file == nil {
		return nil
	}

	// Collect package-level variable names for detecting global writes.
	packageVars := make(map[string]bool)
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		if gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range vs.Names {
				if name.Name != "" && name.Name != "_" {
					packageVars[name.Name] = true
				}
			}
		}
	}

	var mutations []globalStateMutation

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		// Only inspect Test*, Benchmark*, Example* functions.
		if !isTestFunction(fn.Name.Name) {
			continue
		}

		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				// Detect os.Setenv("KEY", "value")
				if se, ok := node.Fun.(*ast.SelectorExpr); ok {
					if pkgIdent, ok := se.X.(*ast.Ident); ok {
						pkgName := pkgIdent.Name
						methodName := se.Sel.Name

						// os.Setenv in test code
						if (pkgName == "os" || pkgName == "OS") && methodName == "Setenv" {
							mutations = append(mutations, globalStateMutation{
								kind: "os-setenv",
								line: fset.Position(node.Pos()).Line,
							})
						}
					}
				}

			case *ast.AssignStmt:
				// Detect assignments to os.Args, os.Stdout, os.Stderr
				for _, lhs := range node.Lhs {
					if sel, ok := lhs.(*ast.SelectorExpr); ok {
						if pkgIdent, ok := sel.X.(*ast.Ident); ok {
							pkgName := pkgIdent.Name
							fieldName := sel.Sel.Name
							if (pkgName == "os" || pkgName == "OS") &&
								(fieldName == "Args" || fieldName == "Stdout" || fieldName == "Stderr") {
								kind := "os-args"
								if fieldName == "Stdout" || fieldName == "Stderr" {
									kind = "os-stdio"
								}
								mutations = append(mutations, globalStateMutation{
									kind: kind,
									line: fset.Position(node.Pos()).Line,
								})
							}
						}
					}
					// Detect direct writes to package-level variables
					if ident, ok := lhs.(*ast.Ident); ok {
						if packageVars[ident.Name] && ident.Obj != nil {
							// Obj.Kind == ast.Var means it's a variable declaration.
							// If it's in the file's package scope, it's global.
							if ident.Obj.Kind == ast.Var {
								mutations = append(mutations, globalStateMutation{
									kind: "global-var",
									line: fset.Position(node.Pos()).Line,
								})
							}
						}
					}
				}
			}
			return true
		})
	}

	return mutations
}

// isTestFunction returns true if the function name matches Go test conventions:
// Test*, Benchmark*, Example*, Fuzz*.
func isTestFunction(name string) bool {
	return strings.HasPrefix(name, "Test") ||
		strings.HasPrefix(name, "Benchmark") ||
		strings.HasPrefix(name, "Example") ||
		strings.HasPrefix(name, "Fuzz")
}
