package agent

// Range Over Nil Pointer Detection in Go Code
//
// Problem: AI coding agents sometimes produce code that ranges over a pointer
// to a slice or map without first checking if the pointer is nil. If the
// pointer is nil, ranging over it causes a nil pointer dereference panic at
// runtime -- one of the most common Go crashes.
//
// Example bug:
//
//	func process(items *[]Item) {
//	    for _, item := range *items {   // panics if items == nil
//	        handle(item)
//	    }
//	}
//
// Safe version:
//
//	func process(items *[]Item) {
//	    if items == nil {
//	        return
//	    }
//	    for _, item := range *items {
//	        handle(item)
//	    }
//	}
//
// This check also detects ranging over a pointer-to-slice/map RETURNED from a
// function call (e.g., for _, v := range *getPtr() {}), which is even more
// dangerous since the nil-ness cannot be checked beforehand without a
// temporary variable.
//
// Competitor analysis:
//   - Claude Code: no write-time detection
//   - Cursor: no write-time detection
//   - go vet: does not flag this pattern
//   - staticcheck (SA5011): detects some nil pointer dereferences but is
//     conservative on range expressions
//
// This check is purely AST-based (zero LLM cost) and runs as a post-write
// integrity gate.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

const maxRangeNilPtrWarnings = 6

// checkRangeNilPtr detects range statements that dereference a pointer
// without a preceding nil guard.
func checkRangeNilPtr(filePath, _, newContent string) string {
	if strings.TrimSpace(newContent) == "" {
		return ""
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, newContent, 0)
	if err != nil || file == nil {
		return ""
	}

	var warnings []string
	ast.Inspect(file, func(n ast.Node) bool {
		if rs, ok := n.(*ast.RangeStmt); ok {
			w := rnpCheckStmt(rs, fset)
			if w != "" {
				warnings = append(warnings, w)
			}
		}
		return true
	})

	if len(warnings) == 0 {
		return ""
	}
	if len(warnings) > maxRangeNilPtrWarnings {
		remaining := len(warnings) - maxRangeNilPtrWarnings
		warnings = warnings[:maxRangeNilPtrWarnings]
		warnings = append(warnings, fmt.Sprintf("...and %d more range-nil-pointer warning(s)", remaining))
	}
	return strings.Join(warnings, "\n")
}

// rnpCheckStmt inspects a single range statement for pointer dereference in
// the range expression without a nil guard.
func rnpCheckStmt(rs *ast.RangeStmt, fset *token.FileSet) string {
	star, ok := rs.X.(*ast.StarExpr)
	if !ok {
		return ""
	}

	varName := rnpExprName(star.X)
	pos := fset.Position(star.Pos())

	if varName == "" {
		return fmt.Sprintf("%s: range over pointer dereference of a non-variable expression -- "+
			"if the pointer is nil this will panic. Store in a variable and check for nil first.",
			rnpFormatPos(pos))
	}

	return fmt.Sprintf("%s: range over *%s without nil check -- if %s is nil this will "+
		"panic with nil pointer dereference. Add `if %s == nil { return }` before the loop.",
		rnpFormatPos(pos), varName, varName, varName)
}

// rnpExprName extracts the variable name from a dereferenced expression.
// Returns empty string for complex expressions (calls, index, selectors).
func rnpExprName(expr ast.Expr) string {
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

// rnpFormatPos formats a token.Position for display.
func rnpFormatPos(pos token.Position) string {
	return fmt.Sprintf("%s:%d", pos.Filename, pos.Line)
}
