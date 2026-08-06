package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// checkUnusedParam detects Go function parameters that are never referenced
// in the function body. Unused parameters often indicate dead code, copy-paste
// errors, or incomplete refactoring.
//
// This is a pure AST check -- zero LLM cost.
// Heuristics:
//   - Skip exported functions (API stability may require the param)
//   - Skip test files (fixtures commonly have unused params)
//   - Skip functions with no body or <= 1 statement (stubs)
//   - Skip params named _ (blank identifier, intentionally unused)
func checkUnusedParam(filePath, _, src string) []string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, src, 0)
	if err != nil || file == nil {
		return nil
	}

	var warnings []string
	const maxUnusedParamWarnings = 5

	for _, decl := range file.Decls {
		if len(warnings) >= maxUnusedParamWarnings {
			warnings = append(warnings,
				fmt.Sprintf("...and more unused parameter(s) in %s", filePath))
			break
		}
		warnings = append(warnings, upCheckFunc(fset, filePath, decl)...)
	}
	return warnings
}

// upCheckFunc checks a single function declaration for unused parameters.
func upCheckFunc(fset *token.FileSet, filePath string, decl ast.Decl) []string {
	fd, ok := decl.(*ast.FuncDecl)
	if !ok || fd.Body == nil || fd.Type.Params == nil {
		return nil
	}
	// Skip stubs
	if len(fd.Body.List) <= 1 {
		return nil
	}
	// Skip exported functions
	if fd.Name.IsExported() {
		return nil
	}
	// Skip test files
	if strings.HasSuffix(filePath, "_test.go") {
		return nil
	}

	used := make(map[string]bool)
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok {
			used[ident.Name] = true
		}
		return true
	})

	var warnings []string
	for _, field := range fd.Type.Params.List {
		for _, name := range field.Names {
			if name.Name == "_" || used[name.Name] {
				continue
			}
			pos := fset.Position(name.Pos())
			warnings = append(warnings, fmt.Sprintf(
				"%s: parameter '%s' is never used in function '%s' "+
					"-- consider removing it or renaming to '_'",
				upFormatPos(pos), name.Name, fd.Name.Name))
		}
	}
	return warnings
}

// upFormatPos formats a token.Position for display.
func upFormatPos(pos token.Position) string {
	return fmt.Sprintf("%s:%d", pos.Filename, pos.Line)
}
