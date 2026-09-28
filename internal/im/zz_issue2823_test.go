package im

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// zz_issue2823_test.go - probe for #2823: matrix_adapter must detect the
// "no bound session" case via the sentinel (errors.Is(err, ErrNoSessionBound)),
// not by comparing error text. The old compare used the wrong literal
// ("no session bound" vs the sentinel's "no active session bound") so it never
// matched and healthy adapters got flagged warning on every inbound message.
// AST-level assertions: string literals in comments do not appear as BasicLit,
// so the fix's explanatory comment cannot satisfy or break these checks.
func TestIssue2823MatrixUsesSentinelComparison(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "matrix_adapter.go", nil, 0)
	if err != nil {
		t.Fatalf("parse matrix_adapter.go: %v", err)
	}

	brokenLiteral := "\"no session bound\""
	hasErrorsIsSentinel := false
	ast.Inspect(f, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.BasicLit:
			if node.Kind == token.STRING && node.Value == brokenLiteral {
				t.Errorf("broken error-text literal %s still present at %s", brokenLiteral, fset.Position(node.Pos()))
			}
		case *ast.CallExpr:
			if sel, ok := node.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Is" && len(node.Args) == 2 {
				if id, ok := node.Args[1].(*ast.Ident); ok && id.Name == "ErrNoSessionBound" {
					hasErrorsIsSentinel = true
				}
			}
		}
		return true
	})
	if !hasErrorsIsSentinel {
		t.Error("matrix_adapter.go must use errors.Is(err, ErrNoSessionBound) for the no-bound-session case")
	}
}
