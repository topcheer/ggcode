package agent

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"
)

type panicInstance struct {
	posStr      string
	fingerprint string
}

func checkPanicSafety(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	if isTestFile(filePath) {
		return nil
	}

	if isCmdPackage(filePath) {
		return nil
	}

	newInstances := findBarePanics(newContent)
	if len(newInstances) == 0 {
		return nil
	}

	var oldSeen map[string]bool
	if strings.TrimSpace(oldContent) != "" {
		for _, iss := range findBarePanics(oldContent) {
			if oldSeen == nil {
				oldSeen = make(map[string]bool)
			}
			oldSeen[iss.fingerprint] = true
		}
	}

	var warnings []string
	for _, inst := range newInstances {
		if oldSeen != nil && oldSeen[inst.fingerprint] {
			continue
		}
		msg := "Bare `panic()` at " + inst.posStr + " in library code. " +
			"panic() crashes the entire process if unrecovered (especially " +
			"dangerous in goroutines where there is no caller to recover), " +
			"skips error-handling logic, and makes the function untestable. " +
			"Return an error instead: `return fmt.Errorf(\"...\")`. " +
			"If this is a Must-style helper (e.g., MustCompile), consider " +
			"naming it explicitly. Use recover() only in middleware or " +
			"top-level boundary handlers."
		warnings = append(warnings, msg)
	}

	return warnings
}

func findBarePanics(src string) []panicInstance {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	file, fset, err := parseGoSource("", src, 0)
	if err != nil || file == nil {
		return nil
	}

	var instances []panicInstance

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:

			if d.Name != nil && (d.Name.Name == "main" || d.Name.Name == "init") {
				continue
			}

			if d.Name != nil && strings.HasPrefix(d.Name.Name, "Must") {
				continue
			}
			instances = append(instances, findPanicsInBody(d.Body, fset, d.Name.Name)...)

		case *ast.GenDecl:

			ast.Inspect(d, func(n ast.Node) bool {
				if fl, ok := n.(*ast.FuncLit); ok {
					instances = append(instances, findPanicsInBody(fl.Body, fset, "<funclit>")...)
				}
				return true
			})
		}
	}

	return instances
}

func findPanicsInBody(body *ast.BlockStmt, fset *token.FileSet, funcName string) []panicInstance {
	if body == nil {
		return nil
	}

	hasRecover := false
	var nested []panicInstance
	ast.Inspect(body, func(n ast.Node) bool {

		if fl, ok := n.(*ast.FuncLit); ok {
			nested = append(nested, findPanicsInBody(fl.Body, fset, "<funclit>")...)
			return false
		}
		deferStmt, ok := n.(*ast.DeferStmt)
		if !ok {
			return true
		}

		ast.Inspect(deferStmt.Call, func(dn ast.Node) bool {
			if call, ok := dn.(*ast.CallExpr); ok {
				if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "recover" {
					hasRecover = true
					return false
				}
			}
			return true
		})
		return !hasRecover
	})

	if hasRecover {

		return nested
	}

	var instances []panicInstance

	ast.Inspect(body, func(n ast.Node) bool {

		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		ident, ok := call.Fun.(*ast.Ident)
		if !ok || ident.Name != "panic" {
			return true
		}

		if ident.Obj != nil {
			return true
		}

		instances = append(instances, panicInstance{
			posStr: fset.Position(call.Pos()).String(),

			fingerprint: funcName + "|" + normalizePanicCallText(call, fset),
		})

		return true
	})

	return append(instances, nested...)
}

func normalizePanicCallText(call *ast.CallExpr, fset *token.FileSet) string {
	parts := make([]string, 0, len(call.Args))
	for _, arg := range call.Args {

		parts = append(parts, strings.TrimSpace(renderNode(fset, arg)))
	}
	return "panic(" + strings.Join(parts, ", ") + ")"
}
