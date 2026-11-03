package agent

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"
)

const maxCopylockWarnings = 4

var syncTypes = map[string]bool{
	"sync.Mutex":     true,
	"sync.RWMutex":   true,
	"sync.WaitGroup": true,
	"sync.Cond":      true,
	"sync.Once":      true,
	"sync.Map":       true,
	"sync.Pool":      true,
}

type copylockIssue struct {
	pos      token.Position
	message  string
	funcName string
}

func checkCopylock(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	file, fset, err := parseGoSource(filePath, newContent, 0)
	if err != nil || file == nil {
		return nil
	}

	issues := findCopylockIssues(fset, file)
	if len(issues) == 0 {
		return nil
	}

	oldPositions := collectOldCopylockPositions(filePath, oldContent)

	var totalNewIssues int
	for _, issue := range issues {
		if !oldPositions[issue.funcName+"|"+issue.message] {
			totalNewIssues++
		}
	}

	var warnings []string
	for _, issue := range issues {
		if oldPositions[issue.funcName+"|"+issue.message] {
			continue
		}
		msg := fmt.Sprintf("Copylock: %s at %s. ", issue.message, issue.pos)
		msg += "Copying a sync type by value breaks its internal state (locks become "
		msg += "independent, WaitGroup counters reset, sync.Once may run twice). "
		msg += "Pass/return it by pointer (*sync.Type) instead."
		warnings = append(warnings, msg)
		if len(warnings) >= maxCopylockWarnings {
			warnings = append(warnings, fmt.Sprintf("...and %d more copylock issue(s)", totalNewIssues-len(warnings)))
			break
		}
	}

	return warnings
}

func collectOldCopylockPositions(filePath, oldContent string) map[string]bool {
	if strings.TrimSpace(oldContent) == "" {
		return nil
	}
	oldFile, oldFset, err := parseGoSource(filePath, oldContent, 0)
	if err != nil || oldFile == nil {
		return nil
	}
	oldIssues := findCopylockIssues(oldFset, oldFile)
	if len(oldIssues) == 0 {
		return nil
	}

	result := make(map[string]bool, len(oldIssues))
	for _, issue := range oldIssues {
		result[issue.funcName+"|"+issue.message] = true
	}
	return result
}

func findCopylockIssues(fset *token.FileSet, file *ast.File) []copylockIssue {
	var issues []copylockIssue

	lockStructs := findLockContainingStructs(file)

	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			for _, issue := range checkFuncDeclCopylocks(fset, fn, lockStructs) {
				issue.funcName = fn.Name.Name
				issues = append(issues, issue)
			}

		}
	}

	return issues
}

func findLockContainingStructs(file *ast.File) map[string]bool {
	result := make(map[string]bool)

	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name == nil {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				continue
			}
			if structContainsSync(st) {
				result[ts.Name.Name] = true
			}
		}
	}

	return result
}

func structContainsSync(st *ast.StructType) bool {
	for _, field := range st.Fields.List {
		if identifySyncValueType(field.Type) != "" {
			return true
		}
	}
	return false
}

func checkFuncDeclCopylocks(fset *token.FileSet, fn *ast.FuncDecl, lockStructs map[string]bool) []copylockIssue {
	if fn == nil {
		return nil
	}
	var issues []copylockIssue

	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		for _, field := range fn.Recv.List {
			if typeName := syncOrLockStruct(field.Type, lockStructs); typeName != "" {
				pos := fset.Position(field.Pos())
				issues = append(issues, copylockIssue{
					pos:     pos,
					message: fmt.Sprintf("value receiver of type %s (contains sync field)", typeName),
				})
			}
		}
	}

	if fn.Type != nil && fn.Type.Params != nil {
		for _, field := range fn.Type.Params.List {
			if typeName := syncOrLockStruct(field.Type, lockStructs); typeName != "" {
				pos := fset.Position(field.Pos())
				names := fieldNames(field)
				issues = append(issues, copylockIssue{
					pos:     pos,
					message: fmt.Sprintf("value parameter %s of type %s", names, typeName),
				})
			}
		}
	}

	if fn.Type != nil && fn.Type.Results != nil {
		for _, field := range fn.Type.Results.List {
			if typeName := syncOrLockStruct(field.Type, lockStructs); typeName != "" {
				pos := fset.Position(field.Pos())
				issues = append(issues, copylockIssue{
					pos:     pos,
					message: fmt.Sprintf("value return of type %s", typeName),
				})
			}
		}
	}

	return issues
}

func syncOrLockStruct(expr ast.Expr, lockStructs map[string]bool) string {
	if name := identifySyncValueType(expr); name != "" {
		return name
	}
	if ident, ok := expr.(*ast.Ident); ok && lockStructs[ident.Name] {
		return ident.Name
	}
	return ""
}

func identifySyncValueType(expr ast.Expr) string {

	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	fullName := pkgIdent.Name + "." + sel.Sel.Name
	if syncTypes[fullName] {
		return fullName
	}
	return ""
}

func fieldNames(field *ast.Field) string {
	if len(field.Names) == 0 {
		return "_"
	}
	parts := make([]string, len(field.Names))
	for i, name := range field.Names {
		parts[i] = name.Name
	}
	return strings.Join(parts, ", ")
}
