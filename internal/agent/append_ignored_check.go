package agent

import (
	"fmt"
	"go/ast"
	"path/filepath"
	"strings"
)

const maxAppendIgnoredWarnings = 4

func checkAppendIgnored(filePath, _, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	file, fset, err := parseGoSource(filePath, newContent, 0)
	if err != nil || file == nil {
		return nil
	}

	var warnings []string

	ast.Inspect(file, func(node ast.Node) bool {
		if len(warnings) >= maxAppendIgnoredWarnings {
			return false
		}

		if exprStmt, ok := node.(*ast.ExprStmt); ok {
			if call, ok := exprStmt.X.(*ast.CallExpr); ok {
				if aiIsBuiltinAppend(call) {
					warnings = append(warnings, fmt.Sprintf(
						"append() return value is discarded at %s -- the original slice is unchanged. "+
							"Assign the result: `slice = append(slice, item)`.",
						fset.Position(call.Pos())))
					return true
				}
			}
		}

		return true
	})

	if len(warnings) >= maxAppendIgnoredWarnings {
		warnings = append(warnings, "...and possibly more discarded append() calls (capped)")
	}

	return warnings
}

func aiIsBuiltinAppend(call *ast.CallExpr) bool {
	ident, ok := call.Fun.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == "append"
}
