package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const testIsolationMaxWarnings = 5

func checkTestIsolation(filePath, oldContent, newContent string) string {
	if !strings.HasSuffix(filePath, "_test.go") {
		return ""
	}
	if strings.TrimSpace(newContent) == "" {
		return ""
	}

	oldViolations := findGlobalStateMutations(filePath, oldContent)
	newViolations := findGlobalStateMutations(filePath, newContent)

	typeCounts := computeNetCounts(newViolations, oldViolations)

	anyIncrease := false
	introduced := 0
	for _, c := range typeCounts {
		if c > 0 {
			anyIncrease = true
		}
		if c > 0 {
			introduced += c
		}
	}
	if !anyIncrease {
		return ""
	}
	details := formatIsolationDetails(typeCounts)

	if len(details) == 0 {
		return ""
	}

	sort.Strings(details)
	if len(details) > testIsolationMaxWarnings {
		details = details[:testIsolationMaxWarnings]
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Test isolation risk: %d new global-state mutation(s) detected in test code. "+
		"Mutating global state makes tests non-hermetic, order-dependent, and flaky under -parallel.\n", introduced))
	for _, d := range details {
		b.WriteString("  - ")
		b.WriteString(d)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func computeNetCounts(newViolations, oldViolations []globalStateMutation) map[string]int {
	typeCounts := map[string]int{}
	for _, v := range newViolations {
		typeCounts[v.kind]++
	}
	for _, v := range oldViolations {
		typeCounts[v.kind]--
	}
	return typeCounts
}

func formatIsolationDetails(typeCounts map[string]int) []string {
	kindMessages := map[string]string{
		"os-setenv":  "os.Setenv() call(s) - use t.Setenv() which auto-restores env after the test and prevents parallel env races",
		"os-args":    "os.Args mutation(s) - save/restore with defer to prevent test pollution",
		"os-stdio":   "os.Stdout/Stderr mutation(s) - restore with defer to prevent output capture issues in other tests",
		"global-var": "package-level variable mutation(s) from test function(s) - use local variables or t.Cleanup to restore",
	}
	var details []string
	for kind, count := range typeCounts {
		if count <= 0 {
			continue
		}
		if msg, ok := kindMessages[kind]; ok {
			details = append(details, fmt.Sprintf("%d %s", count, msg))
		}
	}
	return details
}

type globalStateMutation struct {
	kind string
	line int
}

func findGlobalStateMutations(filename, src string) []globalStateMutation {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	file, fset, err := parseGoSource(filename, src, 0)
	if err != nil || file == nil {
		return nil
	}

	packageVars := collectPackageVarNames(file)
	for n := range collectSiblingPackageVarNames(filename) {
		packageVars[n] = true
	}

	var mutations []globalStateMutation
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !isTestFunction(fn.Name.Name) {
			continue
		}

		if fn.Name.Name == "TestMain" {
			continue
		}
		mutations = append(mutations, inspectTestFuncBody(fn.Body, packageVars, fset)...)
	}
	return mutations
}

func collectPackageVarNames(file *ast.File) map[string]bool {
	packageVars := make(map[string]bool)
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range vs.Names {
				if name.Name != "" && name.Name != "_" {
					packageVars[name.Name] = true
				}
			}
		}
	}
	return packageVars
}

func inspectTestFuncBody(body *ast.BlockStmt, packageVars map[string]bool, fset *token.FileSet) []globalStateMutation {

	restores := collectRestoredVars(body)
	var mutations []globalStateMutation
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			if m := detectOSSetenvCall(node, fset); m != nil {
				mutations = append(mutations, *m)
			}
		case *ast.AssignStmt:
			mutations = append(mutations, detectAssignMutations(node, packageVars, restores, fset)...)
		}
		return true
	})
	return mutations
}

func collectRestoredVars(body *ast.BlockStmt) map[string]bool {
	restored := map[string]bool{}
	var walkDefer bool
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.DeferStmt:
			walkDefer = true
			ast.Inspect(node.Call, func(m ast.Node) bool {
				if fl, ok := m.(*ast.FuncLit); ok {
					collectAssignedNames(fl.Body, restored)
				}
				return true
			})
			walkDefer = false
		case *ast.CallExpr:
			if !walkDefer {
				if se, ok := node.Fun.(*ast.SelectorExpr); ok && se.Sel != nil && se.Sel.Name == "Cleanup" {
					for _, arg := range node.Args {
						ast.Inspect(arg, func(m ast.Node) bool {
							if fl, ok := m.(*ast.FuncLit); ok {
								collectAssignedNames(fl.Body, restored)
							}
							return true
						})
					}
				}
			}
		}
		return true
	})
	if len(restored) == 0 {
		return nil
	}

	snapshotted := map[string]bool{}
	var outside func(n ast.Node) bool
	outside = func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		if as, ok := n.(*ast.AssignStmt); ok {
			for _, lhs := range as.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && restored[id.Name] {
					snapshotted[id.Name] = true
				}
			}
		}
		return true
	}
	ast.Inspect(body, outside)
	return snapshotted
}

func collectAssignedNames(body ast.Node, into map[string]bool) {
	if body == nil {
		return
	}
	ast.Inspect(body, func(n ast.Node) bool {
		if as, ok := n.(*ast.AssignStmt); ok {
			for _, lhs := range as.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Name != "" && id.Name != "_" {
					into[id.Name] = true
				}
			}
		}
		return true
	})
}

func collectSiblingPackageVarNames(testFilePath string) map[string]bool {
	names := map[string]bool{}
	dir := filepath.Dir(testFilePath)
	if dir == "" || dir == "." {
		return names
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return names
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(dir, n), nil, 0)
		if err != nil || f == nil {
			continue
		}
		for name := range collectPackageVarNames(f) {
			names[name] = true
		}
	}
	return names
}

func detectOSSetenvCall(node *ast.CallExpr, fset *token.FileSet) *globalStateMutation {
	se, ok := node.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	pkgIdent, ok := se.X.(*ast.Ident)
	if !ok {
		return nil
	}
	if (pkgIdent.Name == "os" || pkgIdent.Name == "OS") && se.Sel.Name == "Setenv" {
		return &globalStateMutation{
			kind: "os-setenv",
			line: fset.Position(node.Pos()).Line,
		}
	}
	return nil
}

func detectAssignMutations(node *ast.AssignStmt, packageVars map[string]bool, restores map[string]bool, fset *token.FileSet) []globalStateMutation {
	var mutations []globalStateMutation
	for _, lhs := range node.Lhs {
		if id, ok := lhs.(*ast.Ident); ok && restores != nil && restores[id.Name] {
			continue
		}
		if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel != nil && restores != nil && restores["os."+sel.Sel.Name] {
			continue
		}
		if m := detectOSGlobalAssignment(lhs, fset); m != nil {
			mutations = append(mutations, *m)
			continue
		}
		if m := detectPackageVarAssignment(lhs, packageVars, fset); m != nil {
			mutations = append(mutations, *m)
		}
	}
	return mutations
}

func detectOSGlobalAssignment(lhs ast.Expr, fset *token.FileSet) *globalStateMutation {
	sel, ok := lhs.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil
	}
	if pkgIdent.Name != "os" && pkgIdent.Name != "OS" {
		return nil
	}
	field := sel.Sel.Name
	switch field {
	case "Args":
		return &globalStateMutation{kind: "os-args", line: fset.Position(lhs.Pos()).Line}
	case "Stdout", "Stderr":
		return &globalStateMutation{kind: "os-stdio", line: fset.Position(lhs.Pos()).Line}
	}
	return nil
}

func detectPackageVarAssignment(lhs ast.Expr, packageVars map[string]bool, fset *token.FileSet) *globalStateMutation {
	ident, ok := lhs.(*ast.Ident)
	if !ok || !packageVars[ident.Name] {
		return nil
	}

	return &globalStateMutation{kind: "global-var", line: fset.Position(lhs.Pos()).Line}
}

func isTestFunction(name string) bool {
	return strings.HasPrefix(name, "Test") ||
		strings.HasPrefix(name, "Benchmark") ||
		strings.HasPrefix(name, "Example") ||
		strings.HasPrefix(name, "Fuzz")
}
