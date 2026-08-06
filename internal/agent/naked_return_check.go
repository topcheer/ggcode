package agent

// Naked return detection for Go files.
//
// Problem: Go allows "naked" returns (bare `return` with no values) in
// functions that use named return values. While harmless in short functions,
// naked returns in long functions are a well-known readability and correctness
// hazard:
//
//	func process(data []byte) (result *Record, err error) {
//	    // ... 40 lines of code ...
//	    result = parse(data)
//	    // ... more code ...
//	    if bad {
//	        return  // naked - reader must scan all named returns to know what's returned
//	    }
//	    // ...
//	    return  // which values are set? easy to miss one
//	}
//
// The Go community consensus (Effective Go, Go Code Review Comments):
//   lines long, naked returns should be replaced with explicit returns."
//
// go vet and staticcheck do NOT detect this pattern. golangci-lint's `golint`
// used to warn but is deprecated. `revive` has a nakedret rule, but it's not
// integrated into AI coding agents.
//
// Competitor analysis:
//   - Claude Code: no inline detection
//   - Cursor: lint-on-save may catch via revive, but not inline post-edit
//   - Cline/OpenHands: no detection
//   - Aider: no detection
//   - GitHub Copilot: no post-edit naked return analysis
//
// Approach: AST-based analysis. For each function with named return values,
// find naked return statements. Flag them if the function body exceeds a
// configurable line threshold (default 20 lines per Effective Go guidance).
//
// Delta-aware: only flags NEW naked returns introduced by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

const (
	// nakedReturnLineThreshold is the minimum function body length (in lines)
	// for naked returns to be flagged. Below this, naked returns are considered
	// acceptable per Effective Go guidance.
	nakedReturnLineThreshold = 20
	// maxNakedReturnWarnings caps the number of warnings to avoid flooding.
	maxNakedReturnWarnings = 3
)

// nakedReturnInfo records a naked return occurrence.
type nakedReturnInfo struct {
	funcName string
	line     int
	bodyLen  int
}

// checkNakedReturn detects naked return statements in long Go functions
// that have named return values. Returns warning strings. Delta-aware.
func checkNakedReturn(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}
	if isTestFile(filePath) {
		return nil
	}

	oldIssues := findNakedReturns(filePath, oldContent)
	newIssues := findNakedReturns(filePath, newContent)

	// Delta: compare by funcName since line numbers shift.
	oldSet := make(map[string]bool)
	for _, o := range oldIssues {
		oldSet[o.funcName] = true
	}

	var newNaked []nakedReturnInfo
	for _, n := range newIssues {
		if !oldSet[n.funcName] {
			newNaked = append(newNaked, n)
		}
	}

	if len(newNaked) == 0 {
		return nil
	}

	// Deduplicate by function name, keep first occurrence.
	deduped := make(map[string]bool)
	var warnings []string
	for _, n := range newNaked {
		if deduped[n.funcName] {
			continue
		}
		deduped[n.funcName] = true
		if len(warnings) >= maxNakedReturnWarnings {
			warnings = append(warnings, fmt.Sprintf(
				"...and %d more function(s) with naked returns", len(newNaked)-maxNakedReturnWarnings))
			break
		}
		warnings = append(warnings, fmt.Sprintf(
			"Function %q (%d lines) uses naked return (bare `return` with no values). "+
				"In functions longer than %d lines, naked returns hurt readability - "+
				"the reader must scan all named return values to determine what's returned. "+
				"Use explicit return values (e.g. `return result, err`) for clarity.",
			n.funcName, n.bodyLen, nakedReturnLineThreshold))
	}

	return warnings
}

// findNakedReturns parses Go source and returns naked return occurrences
// in functions with named return values that exceed the line threshold.
func findNakedReturns(filename, src string) []nakedReturnInfo {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil
	}

	var results []nakedReturnInfo
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		// Only functions with named return values can have naked returns.
		if !nrHasNamedReturns(fn) {
			continue
		}

		bodyLen := nrBodyLineCount(fn, fset)
		if bodyLen < nakedReturnLineThreshold {
			continue
		}

		funcName := nrFuncName(fn)
		nrFindNakedReturns(fn.Body, funcName, bodyLen, fset, &results)
	}

	return results
}

// nrHasNamedReturns returns true if the function has at least one named
// return value (enabling naked returns).
func nrHasNamedReturns(fn *ast.FuncDecl) bool {
	if fn.Type == nil || fn.Type.Results == nil {
		return false
	}
	for _, field := range fn.Type.Results.List {
		if len(field.Names) > 0 {
			return true
		}
	}
	return false
}

// nrFuncName returns the function name for display.
func nrFuncName(fn *ast.FuncDecl) string {
	if fn.Name != nil && fn.Name.Name != "" {
		return fn.Name.Name
	}
	return "anonymous"
}

// nrBodyLineCount counts the number of source lines in the function body.
func nrBodyLineCount(fn *ast.FuncDecl, fset *token.FileSet) int {
	if fn.Body == nil {
		return 0
	}
	start := fset.Position(fn.Body.Lbrace).Line
	end := fset.Position(fn.Body.Rbrace).Line
	return end - start + 1
}

// nrFindNakedReturns walks a block looking for bare return statements.
func nrFindNakedReturns(block *ast.BlockStmt, funcName string, bodyLen int,
	fset *token.FileSet, results *[]nakedReturnInfo) {

	if block == nil {
		return
	}
	for _, stmt := range block.List {
		nrInspectStmt(stmt, funcName, bodyLen, fset, results)
	}
}

// nrInspectStmt inspects a statement (and its nested blocks) for naked returns.
func nrInspectStmt(stmt ast.Stmt, funcName string, bodyLen int,
	fset *token.FileSet, results *[]nakedReturnInfo) {

	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		// Naked return: no results listed.
		if len(s.Results) == 0 {
			line := fset.Position(s.Pos()).Line
			*results = append(*results, nakedReturnInfo{
				funcName: funcName,
				line:     line,
				bodyLen:  bodyLen,
			})
		}

	case *ast.IfStmt:
		if s.Body != nil {
			nrFindNakedReturns(s.Body, funcName, bodyLen, fset, results)
		}
		if s.Else != nil {
			nrInspectStmt(s.Else, funcName, bodyLen, fset, results)
		}

	case *ast.ForStmt:
		if s.Body != nil {
			nrFindNakedReturns(s.Body, funcName, bodyLen, fset, results)
		}

	case *ast.RangeStmt:
		if s.Body != nil {
			nrFindNakedReturns(s.Body, funcName, bodyLen, fset, results)
		}

	case *ast.SwitchStmt, *ast.TypeSwitchStmt:
		nrWalkCaseClauses(s, funcName, bodyLen, fset, results)

	case *ast.SelectStmt:
		nrWalkCommClauses(s, funcName, bodyLen, fset, results)

	case *ast.BlockStmt:
		nrFindNakedReturns(s, funcName, bodyLen, fset, results)

	case *ast.LabeledStmt:
		if s.Stmt != nil {
			nrInspectStmt(s.Stmt, funcName, bodyLen, fset, results)
		}
	}
}

// nrWalkCaseClauses iterates case clauses in switch/type-switch statements.
func nrWalkCaseClauses(stmt ast.Stmt, funcName string, bodyLen int,
	fset *token.FileSet, results *[]nakedReturnInfo) {

	var body *ast.BlockStmt
	switch s := stmt.(type) {
	case *ast.SwitchStmt:
		body = s.Body
	case *ast.TypeSwitchStmt:
		body = s.Body
	}
	if body == nil {
		return
	}
	for _, c := range body.List {
		if clause, ok := c.(*ast.CaseClause); ok {
			for _, cs := range clause.Body {
				nrInspectStmt(cs, funcName, bodyLen, fset, results)
			}
		}
	}
}

// nrWalkCommClauses iterates comm clauses in select statements.
func nrWalkCommClauses(s *ast.SelectStmt, funcName string, bodyLen int,
	fset *token.FileSet, results *[]nakedReturnInfo) {

	if s.Body == nil {
		return
	}
	for _, comm := range s.Body.List {
		if clause, ok := comm.(*ast.CommClause); ok {
			for _, cs := range clause.Body {
				nrInspectStmt(cs, funcName, bodyLen, fset, results)
			}
		}
	}
}
