package agent

// Type Switch Exhaustiveness Detection
//
// Problem: AI coding agents frequently write type switches on interface types
// without a default case:
//
//	func handleErr(err error) {
//	    switch e := err.(type) {
//	    case *ValidationError:
//	        return e.Field
//	    case *AuthError:
//	        return e.Reason
//	    }
//	    // Missing default - unknown error types silently return zero-value
//	}
//
// When a new concrete type is added to the interface, the type switch silently
// does nothing for that type. This causes:
//  1. Silent failures: the function returns a zero-value with no error
//  2. Debugging nightmares: the missing branch is invisible in production
//  3. Security risks: error type switches that skip unknown types may mask
//     authentication or validation failures
//
// The idiomatic fix is to add a default case that either returns an error or
// panics with a descriptive message, ensuring no type falls through silently.
//
// Competitor analysis:
//   - Claude Code: no detection
//   - Cursor: no detection (go vet does NOT flag missing type switch cases)
//   - Cline/OpenHands: no detection
//   - Aider: no detection
//   - Windsurf/Devin: no detection
//   - staticcheck: does NOT flag this
//   - exhaustive (3rd-party linter): only checks enums, not type switches
//
// Approach: AST-based analysis. For each *ast.TypeSwitchStmt, check if a
// default case exists. If not, and the switch has 2+ type cases, flag it.
// Delta-aware: only flags switches newly introduced by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
)

// typeSwitchIssue represents one detected type switch exhaustiveness problem.
type typeSwitchIssue struct {
	key     string // dedup key: type-switch:<line relative to enclosing func decl>
	message string
}

// maxTypeSwitchWarnings limits the number of warnings per write.
const maxTypeSwitchWarnings = 3

// checkTypeSwitchExhaustive detects type switches on interfaces that lack a
// default case. Returns warnings for newly-introduced switches only (delta-aware).
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

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, newContent, 0)
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

// filterDeltaTypeSwitches subtracts pre-existing issues from old content,
// returning only issues newly introduced by this edit.
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

// findTypeSwitchIssues walks the AST and collects all type switches that lack
// a default case and have 2+ type cases.
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

// analyzeTypeSwitch checks a single type switch for exhaustiveness.
// funcLine is the declaration line of the enclosing function, used to make
// the dedup key position-relative (see below).
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
	// Dedup key is the switch's line RELATIVE to the enclosing function's
	// declaration line (#1212). Keying on funcName alone made a second
	// switch added to the same function dedup away (silent miss) and a pure
	// function rename re-report the same switch (false positive).
	// Relative line handles all three: a rename keeps lines; insertions
	// ABOVE the function shift funcLine and the switch equally; a newly
	// added second switch inside the function lands on a different relative
	// line. Insertions between funcLine and the switch still re-key
	// (inherent to position-based dedup, bounded by the advisory nature of
	// this detector).
	return &typeSwitchIssue{
		key: "type-switch:" + strconv.Itoa(pos.Line-funcLine),
		message: fmt.Sprintf(
			"Type switch at %s has %d case(s) but no default branch: "+
				"unknown concrete types will silently fall through, potentially returning zero-values or skipping critical logic. "+
				"Add a `default:` case that returns an error or panics with a descriptive message to ensure no type is silently ignored.",
			pos.String(), caseCount),
	}
}

// typeSwitchIssueSet converts a slice of typeSwitchIssue into a set for
// delta-aware deduplication.
func typeSwitchIssueSet(issues []typeSwitchIssue) map[string]bool {
	set := make(map[string]bool, len(issues))
	for _, iss := range issues {
		set[iss.key] = true
	}
	return set
}
