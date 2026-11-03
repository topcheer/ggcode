package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

func checkUnusedParam(filePath, oldContent, src string) []string {
	file, fset, err := parseGoSource(filePath, src, 0)
	if err != nil || file == nil {
		return nil
	}

	var issues []upIssue
	for _, decl := range file.Decls {
		issues = append(issues, upCheckFunc(fset, filePath, decl)...)
	}

	if strings.TrimSpace(oldContent) != "" && len(issues) > 0 {
		oldFset := token.NewFileSet()
		oldFile, perr := parser.ParseFile(oldFset, filePath, oldContent, 0)
		if perr == nil && oldFile != nil {
			oldCounts := make(map[string]int)
			for _, decl := range oldFile.Decls {
				for _, oi := range upCheckFunc(oldFset, filePath, decl) {
					oldCounts[upIssueKey(oi)]++
				}
			}
			seen := make(map[string]int)
			filtered := issues[:0]
			for _, is := range issues {
				key := upIssueKey(is)
				seen[key]++
				if seen[key] <= oldCounts[key] {
					continue
				}
				filtered = append(filtered, is)
			}
			issues = filtered
		}
	}

	if len(issues) == 0 {
		return nil
	}

	const maxUnusedParamWarnings = 5
	var warnings []string
	for i, is := range issues {
		if i >= maxUnusedParamWarnings {
			warnings = append(warnings,
				fmt.Sprintf("...and more unused parameter(s) in %s", filePath))
			break
		}
		warnings = append(warnings, fmt.Sprintf(
			"%s: parameter '%s' is never used in function '%s' "+
				"-- if the signature is not an interface/callback contract, consider renaming it to '_' (never delete a parameter: implementations of Shutdown(ctx)/ServeHTTP/handler-adapter contracts fail to compile without it)",
			upFormatPos(is.pos), is.param, is.funcName))
	}
	return warnings
}

type upIssue struct {
	pos      token.Position
	param    string
	funcName string
}

func upIssueKey(i upIssue) string {
	return i.funcName + ":" + i.param
}

func upCheckFunc(fset *token.FileSet, filePath string, decl ast.Decl) []upIssue {
	fd, ok := decl.(*ast.FuncDecl)
	if !ok || fd.Body == nil || fd.Type.Params == nil {
		return nil
	}

	if len(fd.Body.List) <= 1 {
		return nil
	}

	if fd.Name.IsExported() {
		return nil
	}

	if strings.HasSuffix(filePath, "_test.go") {
		return nil
	}

	paramNames := make(map[string]bool)
	for _, field := range fd.Type.Params.List {
		for _, name := range field.Names {
			if name.Name != "_" {
				paramNames[name.Name] = true
			}
		}
	}
	usedObjs := make(map[*ast.Object]bool)
	unresolved := make(map[string]bool)
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if !ok || !paramNames[ident.Name] {
			return true
		}
		if ident.Obj != nil {
			usedObjs[ident.Obj] = true
		} else {
			unresolved[ident.Name] = true
		}
		return true
	})

	var issues []upIssue
	for _, field := range fd.Type.Params.List {
		for _, name := range field.Names {
			if name.Name == "_" {
				continue
			}
			used := unresolved[name.Name] ||
				(name.Obj != nil && usedObjs[name.Obj])
			if used {
				continue
			}
			issues = append(issues, upIssue{
				pos:      fset.Position(name.Pos()),
				param:    name.Name,
				funcName: fd.Name.Name,
			})
		}
	}
	return issues
}

func upFormatPos(pos token.Position) string {
	return fmt.Sprintf("%s:%d", pos.Filename, pos.Line)
}
