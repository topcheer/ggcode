// goroutine_check is a CI gate that flags goroutine launches without
// safego protection. The v1.3.224 crash audit found ~24 unprotected
// goroutines despite 252 safego.Go call sites - review alone was not
// catching them (see commit 55eb91c1 for the fallout).
//
// A launch is COMPLIANT when any of:
//  1. go safego.Go(name, fn)          - the canonical wrapper
//  2. go safego.Run(name, fn)         - spawn-with-recover variant
//  3. go func() { defer safego.Recover(name); ... }  - manual form;
//     any `defer safego.Recover(...)` statement inside the literal body
//  4. go func() { <body> }() where <body> itself contains ONLY a
//     safego.Run/safego.Go call (go safego.Run(...) with a wrapper)
//
// Everything else must be added to goroutine-allowlist.txt (one path per
// line, # comments allowed) with a justification comment next to it.
//
// Usage: go run ./scripts/dev/goroutine_check.go ./internal ./cmd ./desktop
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

// compliant reports whether a go statement is protected per the rules above.
func compliant(fset *token.FileSet, gostmt *ast.GoStmt) bool {
	call, ok := gostmt.Call.Fun.(*ast.SelectorExpr)
	if ok {
		if pkg, isIdent := call.X.(*ast.Ident); isIdent && pkg.Name == safegoPkg {
			return true // go safego.Go(...) / go safego.Run(...)
		}
	}
	lit, ok := gostmt.Call.Fun.(*ast.FuncLit)
	if !ok {
		return false // go someFunc(...) - cannot see protection at launch site
	}
	return bodyHasSafegoRecover(lit.Body) || bodyHasManualRecover(lit.Body)
}

// bodyHasManualRecover reports whether the body defers a closure that calls
// recover() - the hand-rolled equivalent of safego.Recover (e.g.
// check_registry.go's per-worker guard). Counted as compliant.
func bodyHasManualRecover(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
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

// bodyHasSafegoRecover reports whether the body contains a defer of
// safego.Recover (or a call to safego.Run/Go, which carries its own recover).
func bodyHasSafegoRecover(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.DeferStmt:
			if sel, ok := stmt.Call.Fun.(*ast.SelectorExpr); ok {
				if pkg, isIdent := sel.X.(*ast.Ident); isIdent && pkg.Name == safegoPkg {
					found = true
				}
			}
		case *ast.ExprStmt:
			if call, ok := stmt.X.(*ast.CallExpr); ok {
				if sel, ok2 := call.Fun.(*ast.SelectorExpr); ok2 {
					if pkg, isIdent := sel.X.(*ast.Ident); isIdent && pkg.Name == safegoPkg {
						// #1426-C: the header rule says ONLY safego.Run/Go
						// count (they carry their own recover); the old
						// check accepted ANY safego.X() - safego.SetLogger
						// inside a goroutine body passed the CI gate with
						// no recover at all. Restrict to the two runner
						// entry points per the documented rule.
						if sel.Sel != nil && (sel.Sel.Name == "Run" || sel.Sel.Name == "Go") {
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
