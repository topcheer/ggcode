package agent

// Init Function with Side Effects Detection
//
// Problem: Go's init() functions run at package import time, before main().
// AI coding agents sometimes put I/O operations (file reads, network calls,
// environment variable reads, goroutine launches) in init(), which:
//   1. Makes the package unimportable in test environments without those resources
//   2. Causes import-time panics that are hard to debug
//   3. Hides side effects from the import graph
//   4. Makes the code untestable — you can't mock or skip the side effect
//
// Common anti-patterns flagged:
//
//	func init() {
//	    data, _ := os.ReadFile("config.json")   // I/O at import time
//	    http.Get("http://example.com/health")     // network at import time
//	    go startServer()                           // goroutine at import time
//	    os.Setenv("KEY", "val")                    // env mutation at import time
//	    log.Fatal("cannot proceed")               // terminates process at import
//	}
//
// Recommended fix: Move the logic to an explicit Setup() or New() function
// called from main(), so callers control when side effects happen.
//
// Competitor analysis:
//   - Claude Code / Cursor / OpenHands / Aider: no write-time detection
//   - golangci-lint: gochecknoinits flags ALL init() but not side effects
//   - staticcheck: does not check init() side effects
//
// Approach: AST-based. Walk each init() FuncDecl body, inspect call expressions
// for known side-effect-indicating patterns (os.*, net/http.*, log.Fatal*,
// go statements). Zero LLM cost.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

const maxInitSEWarnings = 5

// isePackagePrefixes lists package names whose presence in an init()
// body strongly suggests an import-time side effect. Keys are the short
// package names as they appear in source code (not import paths).
var isePackagePrefixes = map[string]string{
	"os":     "file/env I/O",
	"http":   "network I/O",
	"ioutil": "file I/O",
	"log":    "logging I/O",
	"time":   "timer/sleep",
}

// iseFmtIOFuncs lists fmt functions that actually perform I/O.
// Pure fmt functions (Sprintf, Errorf, Sprint) do NOT perform I/O
// and should not be flagged as side effects in init().
var iseFmtIOFuncs = map[string]bool{
	"Print": true, "Printf": true, "Println": true,
	"Fprint": true, "Fprintf": true, "Fprintln": true,
	"Scan": true, "Scanf": true, "Scanln": true,
	"Fscan": true, "Fscanf": true, "Fscanln": true,
	"Sscan": true, "Sscanf": true, "Sscanln": true,
}

// iseFuncNames lists specific function calls that are definite side effects
// inside init().
var iseFuncNames = map[string]string{
	"Fatal":   "terminates the process",
	"Fatalf":  "terminates the process",
	"Fatalln": "terminates the process",
	"Panic":   "panics the process",
	"Panicf":  "panics the process",
	"Panicln": "panics the process",
	"Exit":    "terminates the process",
}

// checkInitSideEffects detects init() functions that perform I/O or other
// side effects at package import time.
func checkInitSideEffects(filePath, _, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	src := strings.TrimSpace(newContent)
	if src == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, newContent, 0)
	if err != nil || file == nil {
		return nil
	}

	var warnings []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil || fn.Name.Name != "init" {
			continue
		}
		if fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			iseInspectNode(n, fset, &warnings)
			return true
		})
	}

	if len(warnings) > maxInitSEWarnings {
		truncMsg := fmt.Sprintf("... and %d more init() side-effect warning(s)", len(warnings)-maxInitSEWarnings)
		warnings = warnings[:maxInitSEWarnings]
		warnings = append(warnings, truncMsg)
	}
	return warnings
}

// iseInspectNode checks a single AST node for init() side-effect patterns.
func iseInspectNode(n ast.Node, fset *token.FileSet, warnings *[]string) {
	// Check go statements: `go expr()` launches a goroutine
	if gs, ok := n.(*ast.GoStmt); ok {
		pos := fset.Position(gs.Pos())
		*warnings = append(*warnings,
			fmt.Sprintf("%s:%d: init() launches goroutine (go statement). "+
				"Side effects in init() make packages hard to test and "+
				"can cause import-time failures. Move to an explicit Setup().",
				pos.Filename, pos.Line))
		return
	}

	ce, ok := n.(*ast.CallExpr)
	if !ok {
		return
	}

	// Check selector calls: pkg.Func()
	if se, ok := ce.Fun.(*ast.SelectorExpr); ok {
		iseCheckSelectorCall(se, fset, warnings)
	}
}

// iseCheckSelectorCall inspects a pkg.Func() call for known side-effect patterns.
func iseCheckSelectorCall(se *ast.SelectorExpr, fset *token.FileSet, warnings *[]string) {
	pkgName := iseExtractPkg(se.X)
	funcName := se.Sel.Name

	// Check for Fatal/Panic/Exit in any package
	if desc, found := iseFuncNames[funcName]; found {
		pos := fset.Position(se.Pos())
		*warnings = append(*warnings,
			fmt.Sprintf("%s:%d: init() calls %s() which %s at import time. "+
				"This makes the package unimportable in test environments. "+
				"Return an error instead.",
				pos.Filename, pos.Line, funcName, desc))
		return
	}

	// Check package-prefix patterns
	if pkgName != "" {
		// fmt package: only flag actual I/O functions, not pure functions
		// like Sprintf, Errorf, Sprint that return values without side effects.
		if pkgName == "fmt" {
			if iseFmtIOFuncs[funcName] {
				iseEmitPkgWarning(pkgName+"."+funcName, "I/O or panic", se, fset, warnings)
				return
			}
			return // pure fmt function — no side effect
		}
		if desc, found := isePackagePrefixes[pkgName]; found {
			iseEmitPkgWarning(pkgName+"."+funcName, desc, se, fset, warnings)
			return
		}
	}
}

// iseEmitPkgWarning appends a warning for a package-prefixed side effect.
func iseEmitPkgWarning(call, desc string, se *ast.SelectorExpr, fset *token.FileSet, warnings *[]string) {
	pos := fset.Position(se.Pos())
	*warnings = append(*warnings,
		fmt.Sprintf("%s:%d: init() calls %s (%s). "+
			"I/O and side effects in init() run at import time, "+
			"making the package hard to test and prone to import failures. "+
			"Move to an explicit Setup() or init function that returns an error.",
			pos.Filename, pos.Line, call, desc))
}

// iseExtractPkg extracts the package identifier from an expression.
func iseExtractPkg(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return iseExtractPkg(e.X) + "." + e.Sel.Name
	default:
		return ""
	}
}
