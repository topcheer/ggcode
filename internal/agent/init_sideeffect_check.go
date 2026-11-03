package agent

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"
)

const maxInitSEWarnings = 5

var isePackagePrefixes = map[string]string{
	"os":     "file/env I/O",
	"ioutil": "file I/O",
	"log":    "logging I/O",
}

var isePkgFuncBlacklists = map[string]map[string]string{
	"time": {
		"Sleep": "timer/sleep", "NewTicker": "starts a ticker",
		"Tick": "starts a ticker", "AfterFunc": "starts a timer",
	},
	"http": {
		"Get": "network I/O", "Post": "network I/O", "PostForm": "network I/O",
		"Head": "network I/O", "ListenAndServe": "network I/O",
		"ListenAndServeTLS": "network I/O", "Serve": "network I/O",
	},
}

var iseFmtIOFuncs = map[string]bool{
	"Print": true, "Printf": true, "Println": true,
	"Fprint": true, "Fprintf": true, "Fprintln": true,
	"Scan": true, "Scanf": true, "Scanln": true,
	"Fscan": true, "Fscanf": true, "Fscanln": true,
}

var iseFuncNames = map[string]string{
	"Fatal":   "terminates the process",
	"Fatalf":  "terminates the process",
	"Fatalln": "terminates the process",
	"Panic":   "panics the process",
	"Panicf":  "panics the process",
	"Panicln": "panics the process",
	"Exit":    "terminates the process",
}

func checkInitSideEffects(filePath, _, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	src := strings.TrimSpace(newContent)
	if src == "" {
		return nil
	}

	file, fset, err := parseGoSource(filePath, newContent, 0)
	if err != nil || file == nil {
		return nil
	}

	var warnings []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)

		if !ok || fn.Name == nil || fn.Name.Name != "init" || fn.Recv != nil {
			continue
		}
		if fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			iseInspectNode(n, fset, &warnings)
			return true
		})
	}

	if len(warnings) > maxInitSEWarnings {
		truncMsg := fmt.Sprintf("... and %d more init() side-effect warning(s)", len(warnings)-maxInitSEWarnings)
		warnings = warnings[:maxInitSEWarnings]
		warnings = append(warnings, truncMsg)
	}
	return warnings
}

func iseInspectNode(n ast.Node, fset *token.FileSet, warnings *[]string) {

	if gs, ok := n.(*ast.GoStmt); ok {
		pos := fset.Position(gs.Pos())
		*warnings = append(*warnings,
			fmt.Sprintf("%s:%d: init() launches goroutine (go statement). "+
				"Side effects in init() make packages hard to test and "+
				"can cause import-time failures. Move to an explicit Setup().",
				pos.Filename, pos.Line))
		return
	}

	ce, ok := n.(*ast.CallExpr)
	if !ok {
		return
	}

	if id, ok := ce.Fun.(*ast.Ident); ok && id.Name == "panic" {
		pos := fset.Position(ce.Pos())
		*warnings = append(*warnings,
			fmt.Sprintf("%s:%d: init() calls panic() - the package becomes "+
				"unimportable whenever the guard condition trips. Return an "+
				"error or move the check out of init().",
				pos.Filename, pos.Line))
		return
	}

	if se, ok := ce.Fun.(*ast.SelectorExpr); ok {
		iseCheckSelectorCall(se, fset, warnings)
	}
}

func iseCheckSelectorCall(se *ast.SelectorExpr, fset *token.FileSet, warnings *[]string) {
	pkgName := iseExtractPkg(se.X)
	funcName := se.Sel.Name

	if desc, found := iseFuncNames[funcName]; found && (pkgName == "os" || pkgName == "log") {
		pos := fset.Position(se.Pos())
		*warnings = append(*warnings,
			fmt.Sprintf("%s:%d: init() calls %s() which %s at import time. "+
				"This makes the package unimportable in test environments. "+
				"Return an error instead.",
				pos.Filename, pos.Line, funcName, desc))
		return
	}

	if pkgName != "" {

		if pkgName == "fmt" {
			if iseFmtIOFuncs[funcName] {
				iseEmitPkgWarning(pkgName+"."+funcName, "I/O or panic", se, fset, warnings)
				return
			}
			return
		}
		if bl, ok := isePkgFuncBlacklists[pkgName]; ok {
			if desc, found := bl[funcName]; found {
				iseEmitPkgWarning(pkgName+"."+funcName, desc, se, fset, warnings)
			}
			return
		}
		if desc, found := isePackagePrefixes[pkgName]; found {
			iseEmitPkgWarning(pkgName+"."+funcName, desc, se, fset, warnings)
			return
		}
	}
}

func iseEmitPkgWarning(call, desc string, se *ast.SelectorExpr, fset *token.FileSet, warnings *[]string) {
	pos := fset.Position(se.Pos())
	*warnings = append(*warnings,
		fmt.Sprintf("%s:%d: init() calls %s (%s). "+
			"I/O and side effects in init() run at import time, "+
			"making the package hard to test and prone to import failures. "+
			"Move to an explicit Setup() or init function that returns an error.",
			pos.Filename, pos.Line, call, desc))
}

func iseExtractPkg(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return iseExtractPkg(e.X) + "." + e.Sel.Name
	default:
		return ""
	}
}
