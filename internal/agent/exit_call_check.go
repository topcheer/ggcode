package agent

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"
)

var prematureExitFuncs = map[string]bool{
	"os.Exit":     true,
	"log.Fatal":   true,
	"log.Fatalf":  true,
	"log.Fatalln": true,
	"log.Panic":   true,
	"log.Panicf":  true,
	"log.Panicln": true,
}

type exitCallInfo struct {
	funcName string
	line     int
}

func checkPrematureExit(filePath, oldContent, newContent string) []string {
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

	newCalls := findPrematureExitCalls(filePath, newContent)
	if len(newCalls) == 0 {
		return nil
	}

	if strings.TrimSpace(oldContent) != "" {
		oldCalls := findPrematureExitCalls(filePath, oldContent)
		if len(oldCalls) > 0 {
			oldCount := make(map[string]int)
			for _, c := range oldCalls {
				oldCount[c.funcName]++
			}
			filtered := newCalls[:0]
			newCount := make(map[string]int)
			for _, c := range newCalls {
				newCount[c.funcName]++
				if newCount[c.funcName] <= oldCount[c.funcName] {
					continue
				}
				filtered = append(filtered, c)
			}
			newCalls = filtered
		}
	}

	if len(newCalls) == 0 {
		return nil
	}

	funcCounts := make(map[string]int)
	for _, c := range newCalls {
		funcCounts[c.funcName]++
	}

	var parts []string
	for name, count := range funcCounts {
		if count > 1 {
			parts = append(parts, fmt.Sprintf("%s (%d calls)", name, count))
		} else {
			parts = append(parts, name)
		}
	}
	summary := joinStrings(parts, ", ")

	return []string{
		fmt.Sprintf(
			"Introduced premature exit call(s) (%s) in a non-main function. "+
				"os.Exit/log.Fatal/log.Panic skip deferred cleanup (defer Close/Unlock), "+
				"make the function untestable (kills the test process), and prevent "+
				"callers from handling errors. Return an error instead and let the "+
				"caller decide. If process termination is genuinely needed, move the "+
				"call to main() or a top-level entry point.",
			summary),
	}
}

func findPrematureExitCalls(filename, src string) []exitCallInfo {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	file, fset, err := parseGoSource(filename, src, 0)
	if err != nil || file == nil {
		return nil
	}

	var results []exitCallInfo

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:

			if d.Name.Name == "main" || d.Name.Name == "init" {
				continue
			}
			results = append(results, scanForExitCalls(d.Body, fset)...)
		case *ast.GenDecl:

			ast.Inspect(d, func(n ast.Node) bool {
				if fl, ok := n.(*ast.FuncLit); ok {
					results = append(results, scanForExitCalls(fl.Body, fset)...)
				}
				return true
			})
		}
	}

	ast.Inspect(file, func(n ast.Node) bool {
		if fl, ok := n.(*ast.FuncLit); ok {
			_ = fl
		}
		return true
	})

	return results
}

func scanForExitCalls(body *ast.BlockStmt, fset *token.FileSet) []exitCallInfo {
	if body == nil {
		return nil
	}

	var results []exitCallInfo

	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		name := qualifiedCallName(call)
		if name == "" {
			return true
		}

		if prematureExitFuncs[name] {
			pos := fset.Position(call.Pos())
			results = append(results, exitCallInfo{
				funcName: name,
				line:     pos.Line,
			})
		}

		return true
	})

	return results
}

func qualifiedCallName(call *ast.CallExpr) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return pkg.Name + "." + sel.Sel.Name
}

func isCmdPackage(filePath string) bool {

	parts := strings.Split(filepath.ToSlash(filePath), "/")
	for _, p := range parts {
		if p == "cmd" {
			return true
		}
	}
	return false
}

func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	result := parts[0]
	for _, p := range parts[1:] {
		result += sep + p
	}
	return result
}
