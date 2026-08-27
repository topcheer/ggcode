package agent

// Range loop value copy modification detection for Go files.
//
// Problem: In Go, the range loop value variable is a COPY of the slice/map
// element, not a reference. Modifying fields of the range variable does NOT
// affect the original collection:
//
//	type Item struct { Val int }
//	items := []Item{{1}, {2}, {3}}
//	for _, item := range items {
//	    item.Val = 0  // BUG: modifies the COPY, not items[i]
//	}
//	// items is still [{1}, {2}, {3}]
//
// This is one of the most common Go bugs. It compiles cleanly, passes type
// checking, and silently fails at runtime. go vet does NOT catch it.
// staticcheck does not flag it. The Go spec explicitly states range values
// are copies.
//
// Note: Go 1.22 changed loop variable scoping (per-iteration), but the copy
// semantics for range VALUES were NOT changed. The bug persists in all Go
// versions including 1.26+.
//
// Pointer-element exception, syntax-level best effort (#1157): when the
// range expression ITSELF reveals pointer elements -- composite literals
// typed []*T or map[K]*T, or make([]*T, ...) / append calls carrying that
// shape -- writes through the range value DO reach the original collection
// and no warning is issued. Bare identifiers hide the element type, so those
// ranges keep the default warning behavior even when the true element type
// is *T; this false-positive class is accepted because full accuracy needs
// go/types analysis, not syntax alone.
//
// STATUS: this detector is currently NOT registered in the write-integrity
// check registry (dormant since the fc5c4aad critical-only trim). The notes
// above describe its behavior for a future revival.
//
// Competitor analysis:
//   - Claude Code: no inline detection
//   - Cursor: gopls may flag some cases via diagnostics, not at write time
//   - Cline/OpenHands: reactive only
//   - Aider: no detection
//   - Copilot: no post-edit analysis
//
// Approach: AST-based analysis. Find `for ... range` statements with a value
// variable, then walk the loop body looking for assignments to fields of that
// variable (e.g., `item.Field = ...`). Also detect address-of (&item) passed
// to functions, which is another sign of the copy misunderstanding.
// Delta-aware: only flags patterns newly introduced by this edit.
// Ranges whose expression is syntactically inferable as holding pointers
// (see the pointer-element exception above) are skipped entirely (#1157).

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// rangeCopyInfo records a single range-value-copy modification issue.
type rangeCopyInfo struct {
	valueVar string
	line     int
	field    string // the field being modified, or "" for address-of pattern
}

// checkRangeCopyMod detects Go range loop value copy modification bugs.
// Returns warning strings. Only flags NEW occurrences (delta-aware).
func checkRangeCopyMod(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}
	if isTestFile(filePath) {
		return nil
	}

	oldIssues := findRangeCopyMods(filePath, oldContent)
	newIssues := findRangeCopyMods(filePath, newContent)

	// Delta: compare by (valueVar, field) since line numbers shift.
	type rangeCopySig struct {
		valueVar string
		field    string
	}
	oldSet := make(map[rangeCopySig]bool)
	for _, o := range oldIssues {
		oldSet[rangeCopySig{o.valueVar, o.field}] = true
	}

	var newMods []rangeCopyInfo
	for _, n := range newIssues {
		if !oldSet[rangeCopySig{n.valueVar, n.field}] {
			newMods = append(newMods, n)
		}
	}

	if len(newMods) == 0 {
		return nil
	}

	var warnings []string
	for _, m := range newMods {
		if m.field != "" {
			warnings = append(warnings, fmt.Sprintf(
				"Line %d: range variable '%s' field '%s' is modified, but range values are COPIES of slice elements - this does NOT modify the original slice. Use index-based access: slice[i].%s = ...",
				m.line, m.valueVar, m.field, m.field))
		} else {
			warnings = append(warnings, fmt.Sprintf(
				"Line %d: address of range variable '%s' is taken, but range values are COPIES - modifications through the pointer will NOT affect the original slice. Use &slice[i] instead.",
				m.line, m.valueVar))
		}
	}
	return warnings
}

// findRangeCopyMods scans Go source for range-value-copy modification patterns.
func findRangeCopyMods(filename, src string) []rangeCopyInfo {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return nil
	}

	var issues []rangeCopyInfo

	ast.Inspect(file, func(n ast.Node) bool {
		forStmt, ok := n.(*ast.RangeStmt)
		if !ok {
			return true
		}

		// We need a value variable (the second variable in `for _, v := range`)
		valueIdent, ok := forStmt.Value.(*ast.Ident)
		if !ok || valueIdent.Name == "_" {
			return true
		}

		valueVarName := valueIdent.Name

		// Pointer-element collections such as []*T propagate mutations made
		// through the range value, so suppress reporting for shapes the AST
		// lets us infer confidently (#1157).
		if rangeExprHasPointerElems(forStmt.X) {
			return true
		}

		// Walk the body looking for field modifications and address-of patterns.
		ast.Inspect(forStmt.Body, func(bodyNode ast.Node) bool {
			if field := findRangeFieldAssign(bodyNode, valueVarName); field != "" {
				issues = append(issues, rangeCopyInfo{
					valueVar: valueVarName,
					line:     fset.Position(bodyNode.Pos()).Line,
					field:    field,
				})
			}
			if isRangeValueAddrOf(bodyNode, valueVarName) {
				issues = append(issues, rangeCopyInfo{
					valueVar: valueVarName,
					line:     fset.Position(bodyNode.Pos()).Line,
					field:    "",
				})
			}
			return true
		})

		return true
	})

	return issues
}

// findRangeFieldAssign checks if bodyNode is an assignment to a field of the
// range value variable (e.g., item.Field = x). Returns the field name or "".
func findRangeFieldAssign(bodyNode ast.Node, valueVarName string) string {
	assign, ok := bodyNode.(*ast.AssignStmt)
	if !ok {
		return ""
	}
	for _, lhs := range assign.Lhs {
		sel, ok := lhs.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != valueVarName {
			continue
		}
		return sel.Sel.Name
	}
	return ""
}

// isRangeValueAddrOf checks if bodyNode is a function call where the address
// of the range value variable is passed as an argument (e.g., foo(&item)).
func isRangeValueAddrOf(bodyNode ast.Node, valueVarName string) bool {
	call, ok := bodyNode.(*ast.CallExpr)
	if !ok {
		return false
	}
	for _, arg := range call.Args {
		unary, ok := arg.(*ast.UnaryExpr)
		if !ok || unary.Op != token.AND {
			continue
		}
		ident, ok := unary.X.(*ast.Ident)
		if ok && ident.Name == valueVarName {
			return true
		}
	}
	return false
}

// rangeExprHasPointerElems reports whether a range expression is syntactically
// inferable as holding POINTER elements (#1157): a composite literal typed
// []T / [N]T / map[K]T with *T in the element or value position, a make call
// constructing such a type, or an append call whose leading slice operand has
// that shape (including nested make/append forms). Mutating fields through
// range values of pointer-element collections reaches the original data, so
// such loops must not be warned about. Uninferable expressions (bare
// identifiers, no shape available at syntax level) return false, preserving
// the original warning behavior.
func rangeExprHasPointerElems(expr ast.Expr) bool {
	switch e := ast.Unparen(expr).(type) {
	case *ast.CompositeLit:
		return typeExprHasPointerElem(e.Type)
	case *ast.CallExpr:
		ident, ok := ast.Unparen(e.Fun).(*ast.Ident)
		if !ok {
			return false
		}
		switch ident.Name {
		case "make":
			if len(e.Args) > 0 {
				return typeExprHasPointerElem(e.Args[0])
			}
		case "append":
			if len(e.Args) > 0 {
				// Covers append([]*T{...}, ...) and nested forms like
				// append(make([]*T, 0, n), ...).
				return rangeExprHasPointerElems(e.Args[0])
			}
		}
	}
	return false
}

// typeExprHasPointerElem reports whether a type expression declares POINTER
// values: [N]*T, []*T, or map[K]*T. Keys and length expressions do not matter.
func typeExprHasPointerElem(typeExpr ast.Expr) bool {
	switch t := ast.Unparen(typeExpr).(type) {
	case *ast.ArrayType:
		_, ok := ast.Unparen(t.Elt).(*ast.StarExpr)
		return ok
	case *ast.MapType:
		_, ok := ast.Unparen(t.Value).(*ast.StarExpr)
		return ok
	}
	return false
}
