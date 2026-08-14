package agent

// Bare Panic Safety Detection in Go Code
//
// Problem: AI coding agents frequently use the Go built-in panic() in library
// (non-main) code as an error-handling shortcut. Unlike returning an error,
// panic() has severe consequences:
//
//  1. In goroutines: an unrecovered panic in any goroutine crashes the ENTIRE
//     process immediately, even if the panic occurred in an unrelated subsystem.
//     Unlike errors, panics cannot be handled by the caller.
//  2. Skips deferred cleanup: while deferred functions DO run during panic
//     unwinding, the caller has no way to decide whether to continue or abort.
//  3. Loses error type information: panic(fmt.Sprintf(...)) produces a string,
//     losing the concrete error type so errors.Is()/errors.As() can't match.
//  4. Untestable: a panic in a test kills the test process (unless wrapped in
//     recover), making the function impossible to unit test properly.
//
// The existing exit_call_check.go catches log.Panic/log.Panicf/log.Panicln but
// NOT the Go built-in panic(). This is because panic() is a language built-in
// (not a package-qualified call), so qualifiedCallName() returns "" for it.
// This check fills that gap.
//
// Common LLM failure modes this check catches:
//  1. panic("should never happen") instead of returning an error
//  2. panic(fmt.Sprintf("invalid input: %v", x)) in input validation
//  3. panic(err) instead of propagating the error up the call chain
//  4. panic in a goroutine without recover, causing process-wide crashes
//
// Competitor analysis:
//   - Claude Code: no automatic panic detection
//   - Cursor: go vet does NOT flag panic() in library code
//   - Cline/OpenHands: no detection
//   - Aider: no detection
//   - GitHub Copilot: sometimes warns via lint, inconsistent
//
// Exceptions (panic IS idiomatic):
//   - main() and init() functions: acceptable program termination
//   - Test helpers: t.Fatal covers this, but panic in test helper is sometimes
//     seen (less critical, so we skip test files)
//   - Generated code: we still flag since agents should learn the pattern
//   - Functions that return interfaces with MustXxx() naming convention
//     (MustCompile, MustParse) -- these are idiomatic panic wrappers
//
// Approach: AST-based analysis. For each non-main/init function, find all
// panic() calls. If the function does NOT have a recover() in the same scope
// (i.e., the panic will propagate to the caller), flag it. Delta-aware: only
// flags panic() calls INTRODUCED by this edit.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// panicInstance represents a detected bare panic() call.
type panicInstance struct {
	posStr string // human-readable position string
}

// checkPanicSafety detects bare panic() calls in non-main Go functions without
// recover(). Returns warning strings. Delta-aware: only flags NEW instances.
//
// Parameters:
//   - filePath: path of the written file (used for language detection)
//   - oldContent: the file content before the write ("" for new files)
//   - newContent: the file content after the write
func checkPanicSafety(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	// Skip test files -- panic in test helpers, while not ideal, is lower risk
	// since test failures already stop execution.
	if isTestFile(filePath) {
		return nil
	}

	// Skip cmd/ directories -- main packages where panic is acceptable.
	if isCmdPackage(filePath) {
		return nil
	}

	newInstances := findBarePanics(newContent)
	if len(newInstances) == 0 {
		return nil
	}

	// Delta check: compare against old content positions (fix #142).
	var oldPos map[string]bool
	if strings.TrimSpace(oldContent) != "" {
		for _, iss := range findBarePanics(oldContent) {
			if oldPos == nil {
				oldPos = make(map[string]bool)
			}
			oldPos[iss.posStr] = true
		}
	}

	var warnings []string
	for _, inst := range newInstances {
		if oldPos != nil && oldPos[inst.posStr] {
			continue
		}
		msg := "Bare `panic()` at " + inst.posStr + " in library code. " +
			"panic() crashes the entire process if unrecovered (especially " +
			"dangerous in goroutines where there is no caller to recover), " +
			"skips error-handling logic, and makes the function untestable. " +
			"Return an error instead: `return fmt.Errorf(\"...\")`. " +
			"If this is a Must-style helper (e.g., MustCompile), consider " +
			"naming it explicitly. Use recover() only in middleware or " +
			"top-level boundary handlers."
		warnings = append(warnings, msg)
	}

	return warnings
}

// countBarePanics returns the number of bare panic() calls in Go source.
func countBarePanics(src string) int {
	return len(findBarePanics(src))
}

// findBarePanics parses Go source and returns all bare panic() calls found in
// non-main/init functions that lack recover() in the same function scope.
func findBarePanics(src string) []panicInstance {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil || file == nil {
		return nil
	}

	var instances []panicInstance

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			// Skip main() and init() -- panic is acceptable in entry points.
			if d.Name != nil && (d.Name.Name == "main" || d.Name.Name == "init") {
				continue
			}
			instances = append(instances, findPanicsInBody(d.Body, fset)...)

		case *ast.GenDecl:
			// GenDecl may contain function literals in var initializers.
			ast.Inspect(d, func(n ast.Node) bool {
				if fl, ok := n.(*ast.FuncLit); ok {
					instances = append(instances, findPanicsInBody(fl.Body, fset)...)
				}
				return true
			})
		}
	}

	return instances
}

// findPanicsInBody walks a function body and returns all bare panic() calls
// that are NOT guarded by a recover() in the same function scope.
func findPanicsInBody(body *ast.BlockStmt, fset *token.FileSet) []panicInstance {
	if body == nil {
		return nil
	}

	// Check if this function has a recover() call inside a defer.
	// Only recover() directly inside a deferred function catches panics;
	// a bare recover() outside defer is a no-op.
	hasRecover := false
	ast.Inspect(body, func(n ast.Node) bool {
		deferStmt, ok := n.(*ast.DeferStmt)
		if !ok {
			return true
		}
		// Check if the deferred function contains a recover() call.
		ast.Inspect(deferStmt.Call, func(dn ast.Node) bool {
			if call, ok := dn.(*ast.CallExpr); ok {
				if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "recover" {
					hasRecover = true
					return false
				}
			}
			return true
		})
		return !hasRecover
	})

	if hasRecover {
		return nil // function handles its own panics
	}

	var instances []panicInstance

	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		// Check for bare panic() -- Fun is an *ast.Ident with name "panic".
		ident, ok := call.Fun.(*ast.Ident)
		if !ok || ident.Name != "panic" {
			return true
		}

		// Ensure this is the built-in panic, not a user-defined function.
		// The built-in panic has no object attached (ident.Obj == nil) when
		// parsed without type information. A user-defined panic function would
		// have a declaration in the same file. We check ident.Obj == nil as a
		// heuristic: if it's nil, it's the built-in.
		if ident.Obj != nil {
			return true // user-defined function named "panic", skip
		}

		instances = append(instances, panicInstance{
			posStr: fset.Position(call.Pos()).String(),
		})

		return true
	})

	return instances
}
