package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

type rangeCopyInfo struct {
	valueVar string
	line     int
	field    string
}

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

func findRangeCopyMods(filename, src string) []rangeCopyInfo {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	file, fset, err := parseGoSource(filename, src, parser.ParseComments)
	if err != nil {
		return nil
	}

	var issues []rangeCopyInfo

	ast.Inspect(file, func(n ast.Node) bool {
		forStmt, ok := n.(*ast.RangeStmt)
		if !ok {
			return true
		}

		valueIdent, ok := forStmt.Value.(*ast.Ident)
		if !ok || valueIdent.Name == "_" {
			return true
		}

		valueVarName := valueIdent.Name

		if rangeExprHasPointerElems(forStmt.X) {
			return true
		}

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

				return rangeExprHasPointerElems(e.Args[0])
			}
		}
	}
	return false
}

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
