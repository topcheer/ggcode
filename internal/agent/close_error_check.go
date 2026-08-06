package agent

// Ignored Error from Close() Detection (Check #78)
//
// Problem: In Go, `defer file.Close()` silently discards the error returned
// by Close(). For read-only files this is usually fine, but for writers
// (os.Create, os.OpenFile with O_WRONLY/O_RDWR) the Close error may indicate
// a failed flush (e.g. ENOSPC on write-back) - data loss that goes unnoticed.
//
// Common anti-patterns flagged:
//
//	defer file.Close()               // BUG: write error lost
//	defer w.Close()                  // BUG: if w is a Writer
//
// Recommended fix:
//
//	defer func() {
//	    if err := file.Close(); err != nil {
//	        log.Printf("close failed: %v", err)
//	    }
// }()
//
// Competitor analysis:
//   - Claude Code / Cursor / OpenHands / Cline / Aider: no write-time detection
//   - golangci-lint: errcheck can flag this but only at lint time, not write time
//   - staticcheck: does not flag defer .Close() specifically
//
// Approach: AST-based analysis. For each defer statement:
// 1. Check if it's `defer <expr>.Close()` (a CallExpr on a method named Close)
// 2. Verify it's NOT inside a closure (defer func(){ ... }() is fine)
// 3. Flag it as a potential ignored close error
//
// To minimize false positives, we only flag when the deferred call is a bare
// method call (no assignment, no error handling wrapper).
//
// Zero LLM cost -- pure AST pattern matching with Go's standard library.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

const maxCloseErrWarnings = 5

// checkCloseErrorIgnored detects defer statements that call .Close()
// without handling the returned error.
func checkCloseErrorIgnored(filePath, _, newContent string) []string {
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
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			ds, ok := n.(*ast.DeferStmt)
			if !ok {
				return true
			}
			ceCloseInspect(ds, fset, &warnings)
			return true
		})
	}

	if len(warnings) > maxCloseErrWarnings {
		truncMsg := fmt.Sprintf("... and %d more ignored Close() error warning(s)", len(warnings)-maxCloseErrWarnings)
		warnings = warnings[:maxCloseErrWarnings]
		warnings = append(warnings, truncMsg)
	}
	return warnings
}

// ceCloseInspect checks a single defer statement for ignored Close() error.
func ceCloseInspect(ds *ast.DeferStmt, fset *token.FileSet, warnings *[]string) {
	// Skip defer of closures: defer func() { ... }()
	if _, isClosure := ds.Call.Fun.(*ast.FuncLit); isClosure {
		return
	}

	// Must be a method call: <receiver>.Close()
	ce, ok := ds.Call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}

	// Method must be named "Close"
	if ce.Sel == nil || ce.Sel.Name != "Close" {
		return
	}

	// Must have no arguments (Close() takes no args)
	if len(ds.Call.Args) != 0 {
		return
	}

	// Get receiver name for the warning message
	recvName := ceCloseRecvName(ce.X)

	pos := fset.Position(ds.Pos())
	*warnings = append(*warnings,
		fmt.Sprintf("%s:%d: defer %s.Close() ignores returned error. "+
			"For writable handles (files, buffers, connections), a Close() error "+
			"may indicate a failed flush or data loss. Consider: "+
			"defer func() { if err := %s.Close(); err != nil { /* handle */ } }()",
			pos.Filename, pos.Line, recvName, recvName))
}

// ceCloseRecvName extracts a human-readable name from the receiver expression.
func ceCloseRecvName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return ceCloseRecvName(e.X) + "." + e.Sel.Name
	case *ast.CallExpr:
		// e.g. os.Create(path) → just show a placeholder
		return "handle"
	default:
		return "handle"
	}
}
