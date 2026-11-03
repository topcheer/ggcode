package agent

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"
)

const (
	unkeyedMaxWarnings  = 4
	unkeyedMinFieldSkip = 1
)

type unkeyedIssue struct {
	line       int
	typeName   string
	fieldCount int
	heuristic  bool
}

func checkUnkeyedStruct(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}
	if isTestFile(filePath) {
		return nil
	}

	newIssues := findUnkeyedStructs(filePath, newContent)
	if len(newIssues) == 0 {
		return nil
	}

	if strings.TrimSpace(oldContent) != "" {
		oldIssues := findUnkeyedStructs(filePath, oldContent)
		if len(oldIssues) > 0 {
			oldCounts := make(map[string]int, len(oldIssues))
			for _, oi := range oldIssues {
				oldCounts[unkeyedIssueKey(oi)]++
			}
			seen := make(map[string]int)
			filtered := newIssues[:0]
			for _, ni := range newIssues {
				key := unkeyedIssueKey(ni)
				seen[key]++
				if seen[key] <= oldCounts[key] {
					continue
				}
				filtered = append(filtered, ni)
			}
			newIssues = filtered
		}
	}

	if len(newIssues) == 0 {
		return nil
	}
	return buildUnkeyedWarnings(newIssues)
}

func unkeyedIssueKey(i unkeyedIssue) string {
	return fmt.Sprintf("unkeyed:%s:%d", i.typeName, i.fieldCount)
}

func buildUnkeyedWarnings(issues []unkeyedIssue) []string {
	var warnings []string
	for i, issue := range issues {
		if i >= unkeyedMaxWarnings {
			warnings = append(warnings, fmt.Sprintf(
				"...and %d more unkeyed struct initialization(s) omitted",
				len(issues)-unkeyedMaxWarnings))
			break
		}
		if issue.heuristic {
			warnings = append(warnings, fmt.Sprintf(
				"Possible unkeyed struct initialization at L%d: %s{%d positional values}. "+
					"If %s is a struct type, prefer initializing by field name - positional struct init breaks silently if fields are reordered or added. "+
					"If it is an array/slice/map, positional elements are valid Go; disregard.",
				issue.line, issue.typeName, issue.fieldCount, issue.typeName))
			continue
		}
		warnings = append(warnings, fmt.Sprintf(
			"Unkeyed struct initialization at L%d: %s{%d positional values}. "+
				"Use field names: %s{Field1: val1, ...}. "+
				"Positional init breaks silently if fields are reordered or added.",
			issue.line, issue.typeName, issue.fieldCount, issue.typeName))
	}
	return warnings
}

func findUnkeyedStructs(filename, src string) []unkeyedIssue {
	file, fset, err := parseGoSource(filename, src, 0)
	if err != nil || file == nil {
		return nil
	}

	structTypes := collectStructTypes(file)

	var issues []unkeyedIssue
	ast.Inspect(file, func(n ast.Node) bool {
		comp, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		issue, found := analyzeComposite(comp, fset, structTypes)
		if found {
			issues = append(issues, issue)
		}
		return true
	})

	return issues
}

func collectStructTypes(file *ast.File) map[string]int {
	result := make(map[string]int)
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				continue
			}
			result[ts.Name.Name] = structFieldCount(st)
		}
	}
	return result
}

func structFieldCount(st *ast.StructType) int {
	if st.Fields == nil {
		return 0
	}
	count := 0
	for _, field := range st.Fields.List {
		if len(field.Names) == 0 {

			count++
			continue
		}
		count += len(field.Names)
	}
	return count
}

func analyzeComposite(comp *ast.CompositeLit, fset *token.FileSet, structTypes map[string]int) (unkeyedIssue, bool) {
	if len(comp.Elts) == 0 {
		return unkeyedIssue{}, false
	}

	typeName := resolveCompositeTypeName(comp)
	if typeName == "" {
		return unkeyedIssue{}, false
	}

	_, isStruct := structTypes[typeName]
	heuristic := false
	if !isStruct {

		if !isQualifiedTypeName(typeName) {
			return unkeyedIssue{}, false
		}
		heuristic = true
	}

	if !allElementsUnkeyed(comp.Elts) {
		return unkeyedIssue{}, false
	}

	if isStruct {
		fieldCount := structTypes[typeName]
		if fieldCount <= unkeyedMinFieldSkip {
			return unkeyedIssue{}, false
		}
	}

	pos := fset.Position(comp.Pos())
	return unkeyedIssue{
		line:       pos.Line,
		typeName:   typeName,
		fieldCount: len(comp.Elts),
		heuristic:  heuristic,
	}, true
}

func resolveCompositeTypeName(comp *ast.CompositeLit) string {
	switch t := comp.Type.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		if ident, ok := t.X.(*ast.Ident); ok {
			return ident.Name + "." + t.Sel.Name
		}
	}
	return ""
}

func isQualifiedTypeName(name string) bool {
	return strings.Contains(name, ".")
}

func allElementsUnkeyed(elts []ast.Expr) bool {
	for _, el := range elts {
		if _, ok := el.(*ast.KeyValueExpr); ok {
			return false
		}
	}
	return true
}
