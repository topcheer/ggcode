package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

const maxContextLeakWarnings = 3

func checkContextLeak(filePath, oldContent, newContent string) string {
	if filepath.Ext(filePath) != ".go" {
		return ""
	}
	if strings.TrimSpace(newContent) == "" {
		return ""
	}

	newAST, fset, err := parseGoSource(filePath, newContent, 0)
	if err != nil {
		return ""
	}

	newLeaks := findContextLeaks(fset, newAST)
	if len(newLeaks) == 0 {
		return ""
	}

	var oldFPs map[string]bool
	if strings.TrimSpace(oldContent) != "" {
		oldFset := token.NewFileSet()
		oldAST, oldErr := parser.ParseFile(oldFset, filePath, oldContent, 0)
		if oldErr == nil {
			oldLeaks := findContextLeaks(oldFset, oldAST)
			if len(oldLeaks) > 0 {
				oldFPs = make(map[string]bool, len(oldLeaks))
				for _, leak := range oldLeaks {
					oldFPs[leak.fingerprint] = true
				}
			}
		}
	}

	var warnings []string
	for _, leak := range newLeaks {
		if oldFPs != nil && oldFPs[leak.fingerprint] {
			continue
		}
		warnings = append(warnings, leak.message)
		if len(warnings) >= maxContextLeakWarnings {
			break
		}
	}

	if len(warnings) == 0 {
		return ""
	}

	var b strings.Builder
	for i, w := range warnings {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(w)
	}
	return b.String()
}

type contextLeak struct {
	fingerprint string
	message     string
}

func findContextLeaks(fset *token.FileSet, file *ast.File) []contextLeak {

	occurrence := map[string]int{}
	var leaks []contextLeak

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		ctxParamName := findContextParam(fn.Type.Params)
		if ctxParamName == "" {
			continue
		}

		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			ident, ok := sel.X.(*ast.Ident)
			if !ok || ident.Name != "context" {
				return true
			}

			fnName := ""
			if sel.Sel.Name == "TODO" {
				fnName = "TODO"
			} else if sel.Sel.Name == "Background" {
				fnName = "Background"
			} else {
				return true
			}

			for _, arg := range call.Args {
				if id, ok := arg.(*ast.Ident); ok && id.Name == ctxParamName {
					return true
				}
			}

			line := fset.Position(call.Pos()).Line
			funcName := ""
			if fn.Name != nil {
				funcName = fn.Name.Name
			}

			recvType := ""
			if fn.Recv != nil && len(fn.Recv.List) > 0 {
				if ft, ok := fn.Recv.List[0].Type.(*ast.StarExpr); ok {
					if id, ok := ft.X.(*ast.Ident); ok {
						recvType = id.Name + "."
					}
				} else if id, ok := fn.Recv.List[0].Type.(*ast.Ident); ok {
					recvType = id.Name + "."
				}
			}
			occurrence[recvType+funcName]++
			leaks = append(leaks, contextLeak{
				fingerprint: fmt.Sprintf("%s%s:%s#%d", recvType, funcName, fnName, occurrence[recvType+funcName]),
				message: fmt.Sprintf(
					"L%d: context.%s() used in function that receives %s context.Context. "+
						"Use the passed %s instead to propagate cancellation, deadlines, and trace context.",
					line, fnName, ctxParamName, ctxParamName),
			})
			return true
		})
	}

	return leaks
}

func findContextParam(params *ast.FieldList) string {
	if params == nil {
		return ""
	}
	for _, field := range params.List {
		if !isContextType(field.Type) {
			continue
		}
		if len(field.Names) == 0 {
			return "ctx"
		}
		for _, name := range field.Names {
			if name.Name != "_" {
				return name.Name
			}
		}
		return "_"
	}
	return ""
}

func isContextType(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == "context" && sel.Sel.Name == "Context"
}
