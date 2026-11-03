package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
)

type typeSwitchIssue struct {
	key     string
	message string
}

const maxTypeSwitchWarnings = 3

func checkTypeSwitchExhaustive(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.HasSuffix(filePath, "_test.go") {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	file, fset, err := parseGoSource(filePath, newContent, 0)
	if err != nil || file == nil {
		return nil
	}

	issues := findTypeSwitchIssues(file, fset)
	if len(issues) == 0 {
		return nil
	}

	issues = filterDeltaTypeSwitches(filePath, oldContent, issues)

	if len(issues) == 0 {
		return nil
	}
	if len(issues) > maxTypeSwitchWarnings {
		issues = issues[:maxTypeSwitchWarnings]
	}
	warnings := make([]string, 0, len(issues))
	for _, iss := range issues {
		warnings = append(warnings, iss.message)
	}
	return warnings
}

func filterDeltaTypeSwitches(filePath, oldContent string, issues []typeSwitchIssue) []typeSwitchIssue {
	if strings.TrimSpace(oldContent) == "" {
		return issues
	}
	oldFset := token.NewFileSet()
	oldFile, oldErr := parser.ParseFile(oldFset, filePath, oldContent, 0)
	if oldErr != nil || oldFile == nil {
		return issues
	}
	oldSet := typeSwitchIssueSet(findTypeSwitchIssues(oldFile, oldFset))
	filtered := issues[:0]
	for _, iss := range issues {
		if !oldSet[iss.key] {
			filtered = append(filtered, iss)
		}
	}
	return filtered
}

func findTypeSwitchIssues(file *ast.File, fset *token.FileSet) []typeSwitchIssue {
	var issues []typeSwitchIssue
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		funcLine := fset.Position(fn.Pos()).Line
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			ts, ok := node.(*ast.TypeSwitchStmt)
			if !ok {
				return true
			}
			issue := analyzeTypeSwitch(ts, fset, funcLine)
			if issue != nil {
				issues = append(issues, *issue)
			}
			return true
		})
	}
	return issues
}

func analyzeTypeSwitch(ts *ast.TypeSwitchStmt, fset *token.FileSet, funcLine int) *typeSwitchIssue {
	caseCount := 0
	hasDefault := false

	for _, stmt := range ts.Body.List {
		cc, ok := stmt.(*ast.CaseClause)
		if !ok {
			continue
		}
		if len(cc.List) == 0 {
			hasDefault = true
		} else {
			caseCount += len(cc.List)
		}
	}

	if hasDefault {
		return nil
	}
	if caseCount < 2 {
		return nil
	}

	pos := fset.Position(ts.Pos())

	return &typeSwitchIssue{
		key: "type-switch:" + strconv.Itoa(pos.Line-funcLine),
		message: fmt.Sprintf(
			"Type switch at %s has %d case(s) but no default branch: "+
				"unknown concrete types will silently fall through, potentially returning zero-values or skipping critical logic. "+
				"Add a `default:` case that returns an error or panics with a descriptive message to ensure no type is silently ignored.",
			pos.String(), caseCount),
	}
}

func typeSwitchIssueSet(issues []typeSwitchIssue) map[string]bool {
	set := make(map[string]bool, len(issues))
	for _, iss := range issues {
		set[iss.key] = true
	}
	return set
}
