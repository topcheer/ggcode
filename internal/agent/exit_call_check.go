package agent

// Premature Exit Call Detection in Go Code
//
// Problem: AI coding agents frequently produce Go code that calls os.Exit(),
// log.Fatal(), log.Fatalf(), log.Fatalln(), or log.Panic() inside helper
// functions, middleware, library packages, or service handlers. These calls:
//
//  1. Skip all deferred functions (defer Body.Close(), defer Unlock(), etc.)
//     causing resource leaks, unflushed buffers, and data corruption.
//  2. Make the function untestable -- the test process is killed, making it
//     impossible to assert error behavior in unit tests.
//  3. Prevent callers from handling or recovering from errors, violating the
//     Go convention of returning errors.
//  4. In long-running services (HTTP servers, workers), a single bad request
//     can crash the entire process.
//
// Correct usage of these functions is limited to:
//   - main() and init() functions (top-level program bootstrap)
//   - TestMain() in _test.go files (test harness entry point)
//   - The top level of cmd/ binaries
//
// Competitor analysis:
//   - Claude Code: no automatic detection (relies on external linters)
//   - Cursor: lint-on-save may catch via go vet, but not at write time
//   - Cline/OpenHands: reactive only -- caught by tests or production incidents
//   - Aider: no automatic detection
//   - Windsurf: no automatic detection
//   - go vet: does not flag os.Exit in non-main functions
//   - staticcheck: does not flag premature exit calls
//   - gosec: does not flag this pattern
//
// None provide INLINE detection at write time. This check provides immediate,
// zero-dependency feedback in <1ms per file using Go's standard library AST
// parser.
//
// Approach: AST-based analysis. Walk the file and find all function/method/
// closure declarations. For each, check if its body contains calls to
// os.Exit(), log.Fatal(), log.Fatalf(), log.Fatalln(), log.Panic(),
// log.Panicf(), or log.Panicln(). If the function is NOT named "main" or
// "init" (and is not in a _test.go file), emit a warning. Delta-aware: only
// flags patterns newly introduced by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// prematureExitFuncs maps fully-qualified function names that unconditionally
// terminate the process. The boolean is always true (used as a set).
var prematureExitFuncs = map[string]bool{
	"os.Exit":     true,
	"log.Fatal":   true,
	"log.Fatalf":  true,
	"log.Fatalln": true,
	"log.Panic":   true,
	"log.Panicf":  true,
	"log.Panicln": true,
}

// exitCallInfo records a single premature exit call with location and function name.
type exitCallInfo struct {
	funcName string // e.g., "os.Exit", "log.Fatal"
	line     int    // 1-based line number (best-effort from AST positions)
}

// checkPrematureExit detects os.Exit/log.Fatal/log.Panic calls in non-main/init
// Go functions. Returns warning strings. Only flags NEW occurrences (delta-aware).
//
// Parameters:
//   - filePath: path of the written file (used for language detection)
//   - oldContent: the file content before the write ("" for new files)
//   - newContent: the file content after the write
func checkPrematureExit(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	// Skip test files -- os.Exit in TestMain or test helpers is acceptable.
	if isTestFile(filePath) {
		return nil
	}

	// Skip files in cmd/ directories -- these are typically main packages
	// where os.Exit is expected (CLI tools, entry points).
	if isCmdPackage(filePath) {
		return nil
	}

	newCalls := findPrematureExitCalls(filePath, newContent)
	if len(newCalls) == 0 {
		return nil
	}

	// Delta-aware: parse old content and subtract pre-existing calls.
	if strings.TrimSpace(oldContent) != "" {
		oldCalls := findPrematureExitCalls(filePath, oldContent)
		if len(oldCalls) > 0 {
			oldCount := make(map[string]int)
			for _, c := range oldCalls {
				oldCount[c.funcName]++
			}
			filtered := newCalls[:0]
			newCount := make(map[string]int)
			for _, c := range newCalls {
				newCount[c.funcName]++
				if newCount[c.funcName] <= oldCount[c.funcName] {
					continue
				}
				filtered = append(filtered, c)
			}
			newCalls = filtered
		}
	}

	if len(newCalls) == 0 {
		return nil
	}

	// Build summary message.
	funcCounts := make(map[string]int)
	for _, c := range newCalls {
		funcCounts[c.funcName]++
	}

	var parts []string
	for name, count := range funcCounts {
		if count > 1 {
			parts = append(parts, fmt.Sprintf("%s (%d calls)", name, count))
		} else {
			parts = append(parts, name)
		}
	}
	summary := joinStrings(parts, ", ")

	return []string{
		fmt.Sprintf(
			"Introduced premature exit call(s) (%s) in a non-main function. "+
				"os.Exit/log.Fatal/log.Panic skip deferred cleanup (defer Close/Unlock), "+
				"make the function untestable (kills the test process), and prevent "+
				"callers from handling errors. Return an error instead and let the "+
				"caller decide. If process termination is genuinely needed, move the "+
				"call to main() or a top-level entry point.",
			summary),
	}
}

// findPrematureExitCalls parses Go source and returns all premature exit calls
// found in non-main/init functions/methods/closures.
func findPrematureExitCalls(filename, src string) []exitCallInfo {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil || file == nil {
		return nil
	}

	var results []exitCallInfo

	// Walk all function declarations and function literals (closures).
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			// Skip main() and init() -- these are the only non-test functions
			// where process exit is acceptable.
			if d.Name.Name == "main" || d.Name.Name == "init" {
				continue
			}
			results = append(results, scanForExitCalls(d.Body, fset)...)
		case *ast.GenDecl:
			// GenDecl (var, const, type, import) -- may contain function literals
			// in var initializers. Walk for FuncLit nodes.
			ast.Inspect(d, func(n ast.Node) bool {
				if fl, ok := n.(*ast.FuncLit); ok {
					results = append(results, scanForExitCalls(fl.Body, fset)...)
				}
				return true
			})
		}
	}

	// Also scan function literals that appear in expressions (not in var decls).
	ast.Inspect(file, func(n ast.Node) bool {
		if fl, ok := n.(*ast.FuncLit); ok {
			_ = fl // FuncLit inside FuncDecl bodies are covered by scanForExitCalls.
		}
		return true
	})

	return results
}

// scanForExitCalls walks a function body (recursively through nested blocks,
// closures, if/switch/for/select) and collects all premature exit calls.
func scanForExitCalls(body *ast.BlockStmt, fset *token.FileSet) []exitCallInfo {
	if body == nil {
		return nil
	}

	var results []exitCallInfo

	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		name := qualifiedCallName(call)
		if name == "" {
			return true
		}

		if prematureExitFuncs[name] {
			pos := fset.Position(call.Pos())
			results = append(results, exitCallInfo{
				funcName: name,
				line:     pos.Line,
			})
		}

		return true
	})

	return results
}

// qualifiedCallName extracts the fully-qualified function name from a call
// expression. Returns "os.Exit" for os.Exit(), "log.Fatal" for log.Fatal(),
// etc. Returns "" for non-package-qualified calls.
func qualifiedCallName(call *ast.CallExpr) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return pkg.Name + "." + sel.Sel.Name
}

// isCmdPackage returns true if the file is in a cmd/ directory (typical Go
// binary entry point).
func isCmdPackage(filePath string) bool {
	// Check if any path component is "cmd".
	parts := strings.Split(filepath.ToSlash(filePath), "/")
	for _, p := range parts {
		if p == "cmd" {
			return true
		}
	}
	return false
}

// joinStrings joins a slice of strings with a separator. This is a small helper
// to avoid importing strings.Join (which is fine but keeps the pattern explicit).
func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	result := parts[0]
	for _, p := range parts[1:] {
		result += sep + p
	}
	return result
}
