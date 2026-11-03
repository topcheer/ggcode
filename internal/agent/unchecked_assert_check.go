package agent

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"
)

type uncheckedAssertInfo struct {
	line int
	expr string
}

func checkUncheckedTypeAssert(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	oldAsserts := findUncheckedAsserts(filePath, oldContent)
	newAsserts := findUncheckedAsserts(filePath, newContent)

	oldCounts := make(map[string]int)
	for _, a := range oldAsserts {
		oldCounts[assertFingerprint(a)]++
	}
	newCounts := make(map[string]int)
	for _, a := range newAsserts {
		newCounts[assertFingerprint(a)]++
	}
	introduced := 0
	for fp, n := range newCounts {
		if d := n - oldCounts[fp]; d > 0 {
			introduced += d
		}
	}
	if introduced == 0 {
		return nil
	}
	noun := "unchecked type assertion"
	if introduced > 1 {
		noun = "unchecked type assertions"
	}

	return []string{
		fmt.Sprintf(
			"Introduced %d %s: x.(T) without comma-ok guard. "+
				"If the assertion fails, Go panics with 'interface conversion'. "+
				"Use the comma-ok form: val, ok := x.(T) and handle !ok gracefully.",
			introduced, noun),
	}
}

func findUncheckedAsserts(filename, src string) []uncheckedAssertInfo {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	file, fset, err := parseGoSource(filename, src, 0)
	if err != nil || file == nil {
		return nil
	}

	okAssertions := make(map[token.Pos]bool)

	allAssertions := make(map[token.Pos]bool)

	ast.Inspect(file, func(n ast.Node) bool {
		ta, ok := n.(*ast.TypeAssertExpr)
		if !ok {
			return true
		}

		if ta.Type == nil {
			return true
		}
		allAssertions[ta.Pos()] = true
		return true
	})

	for _, decl := range file.Decls {
		ast.Inspect(decl, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}

			if len(assign.Lhs) == 2 && len(assign.Rhs) == 1 {
				if ta, ok := assign.Rhs[0].(*ast.TypeAssertExpr); ok {
					okAssertions[ta.Pos()] = true
				}
			}
			return true
		})
	}

	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}

			if len(vs.Names) == 2 && len(vs.Values) == 1 {
				if ta, ok := vs.Values[0].(*ast.TypeAssertExpr); ok {
					okAssertions[ta.Pos()] = true
				}
			}
		}
	}

	var results []uncheckedAssertInfo
	for pos := range allAssertions {
		if okAssertions[pos] {
			continue
		}
		p := fset.Position(pos)
		results = append(results, uncheckedAssertInfo{line: p.Line, expr: assertExprAt(file, pos)})
	}

	return results
}

func assertExprAt(file *ast.File, pos token.Pos) string {
	var target ast.Expr
	ast.Inspect(file, func(n ast.Node) bool {
		if target != nil {
			return false
		}
		if ta, ok := n.(*ast.TypeAssertExpr); ok && ta.Pos() == pos {
			target = ta
			return false
		}
		return true
	})
	if target == nil {
		return fmt.Sprintf("pos:%d", pos)
	}
	ta, ok := target.(*ast.TypeAssertExpr)
	if !ok {
		return fmt.Sprintf("pos:%d", pos)
	}

	return exprText(ta.X) + ".(" + exprText(ta.Type) + ")"
}

func assertFingerprint(a uncheckedAssertInfo) string {
	return a.expr
}
