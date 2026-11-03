package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"

	"strings"
)

const assertionPresenceMaxWarnings = 3

var goAssertionCalls = map[string]bool{

	"Error":   true,
	"Errorf":  true,
	"Fatal":   true,
	"Fatalf":  true,
	"Fail":    true,
	"FailNow": true,
	"Logf":    false,

	"Expect":       true,
	"Eventually":   true,
	"Consistently": true,
}

var goTestSkips = map[string]bool{
	"Skip":    true,
	"Skipf":   true,
	"SkipNow": true,
}

var goGomegaGlobalCalls = map[string]bool{
	"Expect":       true,
	"Eventually":   true,
	"Consistently": true,
}

var goAssertionPkgs = map[string]bool{
	"require": true,
	"assert":  true,
	"quick":   true,
	"check":   true,
	"should":  true,
}

func checkAssertionPresence(filePath, oldContent, newContent string) string {

	if !strings.HasSuffix(filePath, "_test.go") {
		return ""
	}
	if strings.TrimSpace(newContent) == "" {
		return ""
	}

	newAST, fset, err := parseGoSource(filePath, newContent, 0)
	if err != nil {
		return ""
	}

	oldCounts := map[string]int{}
	if strings.TrimSpace(oldContent) != "" {
		oldFset := token.NewFileSet()
		oldAST, err := parser.ParseFile(oldFset, filePath, oldContent, 0)
		if err == nil {
			for name, count := range countAssertionsPerTest(oldFset, oldAST) {
				oldCounts[name] = count
			}
		}
	}

	newCounts := countAssertionsPerTest(fset, newAST)
	var hollow []string
	for name, count := range newCounts {
		if count > 0 {
			continue
		}

		oldCount, existed := oldCounts[name]
		if !existed {

			hollow = append(hollow, name)
		} else if oldCount > 0 {

			hollow = append(hollow, name+" (assertions removed)")
		}

	}

	if len(hollow) == 0 {
		return ""
	}

	sortStrings(hollow)
	if len(hollow) > assertionPresenceMaxWarnings {
		hollow = hollow[:assertionPresenceMaxWarnings]
	}

	var detail string
	if len(hollow) == 1 {
		detail = fmt.Sprintf("Test function %s contains no assertion calls (t.Error, t.Fatal, require.*, assert.*). "+
			"A test without assertions passes trivially and provides no verification value. "+
			"Add meaningful assertions to validate the code under test.", hollow[0])
	} else {
		detail = fmt.Sprintf("The following test functions contain no assertion calls: %s. "+
			"Tests without assertions pass trivially and provide no verification value. "+
			"Add meaningful assertions (t.Error, t.Fatal, require.*, assert.*) to each.", strings.Join(hollow, ", "))
	}
	return detail
}

func countAssertionsPerTest(fset *token.FileSet, file *ast.File) map[string]int {
	result := map[string]int{}

	pkgQualifiers := assertionPkgQualifiers(file)

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		name := fn.Name.Name
		if !strings.HasPrefix(name, "Test") {
			continue
		}

		if name == "TestMain" {
			continue
		}

		if fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
			continue
		}

		testingTName := "t"
		if len(fn.Type.Params.List[0].Names) > 0 {
			testingTName = fn.Type.Params.List[0].Names[0].Name
		}

		count := countAssertionCalls(fn.Body, testingTName, pkgQualifiers)
		result[name] = count
	}

	return result
}

func assertionPkgQualifiers(file *ast.File) map[string]bool {
	qualifiers := make(map[string]bool)
	for _, imp := range file.Imports {
		name := ""
		if imp.Name != nil {
			name = imp.Name.Name
		} else {

			path := strings.Trim(imp.Path.Value, "\"")
			if i := strings.LastIndex(path, "/"); i >= 0 {
				name = path[i+1:]
			} else {
				name = path
			}
		}
		if goAssertionPkgs[name] {
			qualifiers[name] = true
		}
	}
	return qualifiers
}

func countAssertionCalls(body *ast.BlockStmt, testingTName string, pkgQualifiers map[string]bool) int {
	count := 0
	names := map[string]bool{testingTName: true}
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncLit:

			for _, name := range testingTParamNames(node.Type) {
				names[name] = true
			}
		case *ast.CallExpr:
			if isAssertionCall(node, names, pkgQualifiers) {
				count++
			}
		}
		return true
	})
	return count
}

func isAssertionCall(node *ast.CallExpr, names map[string]bool, pkgQualifiers map[string]bool) bool {

	for _, arg := range node.Args {
		if id, ok := arg.(*ast.Ident); ok && names[id.Name] {
			return true
		}
	}

	if sel, ok := node.Fun.(*ast.SelectorExpr); ok {
		method := sel.Sel.Name

		if goTestSkips[method] {
			if ident, ok := sel.X.(*ast.Ident); ok && names[ident.Name] {
				return true
			}
		}
		if goAssertionCalls[method] {

			if ident, ok := sel.X.(*ast.Ident); ok && names[ident.Name] {
				return true
			}
		}

		if pkgIdent, ok := sel.X.(*ast.Ident); ok {
			if pkgQualifiers[pkgIdent.Name] {
				return true
			}

			if pkgIdent.Name == "gomega" && goGomegaGlobalCalls[method] {
				return true
			}
		}
	}

	if id, ok := node.Fun.(*ast.Ident); ok && goGomegaGlobalCalls[id.Name] {
		return true
	}
	return false
}

func testingTParamNames(ftype *ast.FuncType) []string {
	var result []string
	if ftype == nil || ftype.Params == nil {
		return nil
	}
	for _, field := range ftype.Params.List {
		if !isTestingTPtrType(field.Type) {
			continue
		}
		for _, name := range field.Names {
			result = append(result, name.Name)
		}
	}
	return result
}

func isTestingTPtrType(expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "T" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "testing"
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
