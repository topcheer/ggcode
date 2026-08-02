package agent

// Context propagation detection for Go files at write time.
//
// Research basis: AI coding agents (Claude Code, Cursor, Copilot, Cline)
// frequently write context.TODO() or context.Background() inside functions
// that already receive a ctx context.Context parameter. This is a well-known
// code quality issue ("lost context" / "context leak") that breaks:
//   - Request cancellation (the ctx parameter won't propagate cancellation)
//   - Timeout/deadline propagation
//   - Distributed tracing spans
//   - Context-scoped values (tenant ID, request ID, auth tokens)
//
// Real-world impact: in production Go code, lost context causes requests that
// can't be cancelled, orphaned goroutines that run past client disconnects,
// and missing trace spans that make debugging impossible.
//
// go vet does NOT catch this pattern. The "context.TODO in a function that
// has ctx" pattern is a semantic issue that requires understanding the
// function signature.
//
// This check uses go/ast to:
//  1. Identify functions/methods that have a ctx context.Context parameter
//  2. Find calls to context.TODO() or context.Background() within that
//     function body (that don't also pass the ctx parameter)
//  3. Report them as warnings so the agent can fix the issue immediately
//
// The check is delta-aware: only flags context.TODO/Background calls that are
// NEW (introduced by this edit), not pre-existing ones.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// maxContextLeakWarnings limits the number of warnings per write.
const maxContextLeakWarnings = 3

// checkContextLeak detects context.TODO()/context.Background() usage inside
// functions that receive a ctx context.Context parameter. Only flags NEW
// occurrences introduced by this edit (delta-aware).
//
// Returns a non-empty guidance string if issues are detected, or "" if clean.
func checkContextLeak(filePath, oldContent, newContent string) string {
	if filepath.Ext(filePath) != ".go" {
		return ""
	}
	if strings.TrimSpace(newContent) == "" {
		return ""
	}

	fset := token.NewFileSet()
	newAST, err := parser.ParseFile(fset, filePath, newContent, 0)
	if err != nil {
		return ""
	}

	newLeaks := findContextLeaks(fset, newAST)
	if len(newLeaks) == 0 {
		return ""
	}

	// Delta check: find context.TODO/Background calls in the old content.
	var oldLines map[int]bool
	if strings.TrimSpace(oldContent) != "" {
		oldFset := token.NewFileSet()
		oldAST, oldErr := parser.ParseFile(oldFset, filePath, oldContent, 0)
		if oldErr == nil {
			oldLeaks := findContextLeaks(oldFset, oldAST)
			if len(oldLeaks) > 0 {
				oldLines = make(map[int]bool, len(oldLeaks))
				for _, leak := range oldLeaks {
					oldLines[leak.line] = true
				}
			}
		}
	}

	var warnings []string
	for _, leak := range newLeaks {
		if oldLines != nil && oldLines[leak.line] {
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

// contextLeak represents a detected context propagation issue.
type contextLeak struct {
	line    int    // 1-based line number
	message string // human-readable warning
}

// findContextLeaks walks the AST and finds context.TODO()/context.Background()
// calls inside functions that have a ctx context.Context parameter (where the
// ctx parameter is NOT used in the same call expression).
func findContextLeaks(fset *token.FileSet, file *ast.File) []contextLeak {
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

			// Skip if ctx is also passed as an argument (e.g.,
			// context.WithTimeout(ctx, 5s) is fine — it propagates context).
			for _, arg := range call.Args {
				if id, ok := arg.(*ast.Ident); ok && id.Name == ctxParamName {
					return true
				}
			}

			line := fset.Position(call.Pos()).Line
			leaks = append(leaks, contextLeak{
				line: line,
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

// findContextParam checks if a function's parameter list contains a
// context.Context parameter and returns its name. Returns "" if not found.
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

// isContextType returns true if the AST type expression represents context.Context.
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
