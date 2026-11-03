package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

const safegoPkg = "safego"

func main() {
	roots := os.Args[1:]
	if len(roots) == 0 {
		roots = []string{"./internal", "./cmd", "./desktop"}
	}
	allow := loadAllowlist(filepath.Join("scripts", "dev", "goroutine-allowlist.txt"))

	fset := token.NewFileSet()
	violations := 0

	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel := filepath.ToSlash(path)
			if allow[rel] {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				fmt.Printf("PARSE-ERROR %s: %v\n", rel, err)
				violations++
				return nil
			}
			ast.Inspect(file, func(n ast.Node) bool {
				gostmt, ok := n.(*ast.GoStmt)
				if !ok {
					return true
				}
				if !compliant(fset, gostmt) {
					pos := fset.Position(gostmt.Pos())
					fmt.Printf("UNPROTECTED-GOROUTINE %s:%d: %s\n",
						rel, pos.Line, strings.TrimSpace(sourceLine(path, pos.Line)))
					violations++
				}
				return true
			})
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "walk %s: %v\n", root, err)
			os.Exit(2)
		}
	}

	if violations > 0 {
		fmt.Printf("\n%d unprotected goroutine launch(es) found.\n", violations)
		fmt.Println("Wrap in safego.Go / safego.Run, add `defer safego.Recover(name)` inside the")
		fmt.Println("goroutine body, or add the file to scripts/dev/goroutine-allowlist.txt")
		fmt.Println("with a justification.")
		os.Exit(1)
	}
	fmt.Println("goroutine check: OK (all launches safego-protected or allowlisted)")
}

func compliant(fset *token.FileSet, gostmt *ast.GoStmt) bool {
	call, ok := gostmt.Call.Fun.(*ast.SelectorExpr)
	if ok {
		if pkg, isIdent := call.X.(*ast.Ident); isIdent && pkg.Name == safegoPkg {
			return true
		}
	}
	lit, ok := gostmt.Call.Fun.(*ast.FuncLit)
	if !ok {
		return false
	}
	return bodyHasSafegoRecover(lit.Body) || bodyHasManualRecover(lit.Body)
}

func bodyHasManualRecover(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if _, isLit := n.(*ast.FuncLit); isLit {
			return false
		}
		deferStmt, ok := n.(*ast.DeferStmt)
		if !ok {
			return true
		}
		ast.Inspect(deferStmt.Call, func(m ast.Node) bool {
			if id, isIdent := m.(*ast.Ident); isIdent && id.Name == "recover" {
				found = true
			}
			return !found
		})
		return !found
	})
	return found
}

func bodyHasSafegoRecover(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if _, isLit := n.(*ast.FuncLit); isLit {
			return false
		}
		switch stmt := n.(type) {
		case *ast.DeferStmt:
			if sel, ok := stmt.Call.Fun.(*ast.SelectorExpr); ok {
				if pkg, isIdent := sel.X.(*ast.Ident); isIdent && pkg.Name == safegoPkg {
					if sel.Sel != nil && sel.Sel.Name == "Recover" {
						found = true
					}
				}
			}
		case *ast.ExprStmt:
			if call, ok := stmt.X.(*ast.CallExpr); ok {
				if sel, ok2 := call.Fun.(*ast.SelectorExpr); ok2 {
					if pkg, isIdent := sel.X.(*ast.Ident); isIdent && pkg.Name == safegoPkg {

						if sel.Sel != nil && (sel.Sel.Name == "Run" || sel.Sel.Name == "Go") &&
							len(body.List) == 1 {
							found = true
						}
					}
				}
			}
		}
		return !found
	})
	return found
}

func loadAllowlist(path string) map[string]bool {
	out := map[string]bool{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if line != "" {
			out[line] = true
		}
	}
	return out
}

func sourceLine(path string, line int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "<unreadable>"
	}
	lines := strings.Split(string(data), "\n")
	if line-1 < len(lines) {
		return lines[line-1]
	}
	return ""
}
