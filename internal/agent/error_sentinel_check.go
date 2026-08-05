package agent

// Error Sentinel Comparison Detection (Agentic Reliability)
//
// Problem: AI coding agents frequently write direct equality comparisons
// against sentinel errors:
//
//	if err == sql.ErrNoRows { ... }
//	if err != io.EOF { ... }
//	if err == ErrNotFound { ... }
//
// Since Go 1.13, errors can be wrapped with fmt.Errorf("...: %w", err),
// creating an error chain. Direct == comparison only checks the outermost
// error, so a wrapped error will NOT match the sentinel even though the
// underlying cause is the same. This causes subtle, hard-to-diagnose bugs
// where error-handling branches silently fail.
//
// The correct approach is errors.Is(err, sentinel), which traverses the
// entire error chain. Similarly, errors.As(err, &target) should be used
// for type-based matching.
//
// Real-world impact: missing errors.Is checks are one of the most common
// sources of "works in dev, fails in prod" bugs, because wrapping is often
// added later as code matures. The check is especially relevant for AI-
// generated code, which tends to produce == comparisons by default.
//
// Competitor analysis:
//   - Claude Code: no automatic detection (relies on agent judgment)
//   - Cursor: no detection (staticcheck SA1029 may catch some, but requires
//     installed linters and does not run at write time)
//   - Cline/OpenHands: no detection
//   - Aider: no detection
//   - Windsurf: no detection
//   - Devin: no detection
//
// go vet does NOT flag this pattern. staticcheck's SA1029 only fires for
// specific known sentinel comparisons and is not write-time. No AI coding
// agent provides inline detection of error sentinel comparison anti-patterns.
//
// Approach: AST-based analysis of Go source. Scans for BinaryExpr nodes
// (== or !=) where one operand is an error-like variable and the other is
// a sentinel error reference. Pure deterministic analysis, zero LLM cost.
// Delta-aware: only patterns newly introduced by this edit are reported.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// errorSentinelIssue represents one detected sentinel-comparison problem.
type errorSentinelIssue struct {
	key     string // dedup key: sentinel-cmp:pos
	message string
}

// maxErrorSentinelWarnings limits the number of warnings per write.
const maxErrorSentinelWarnings = 3

// knownSentinelNames are error sentinels that do not follow the "Err" prefix
// convention but are extremely common in the Go standard library.
var knownSentinelNames = map[string]bool{
	"EOF":              true, // io.EOF
	"Canceled":         true, // context.Canceled
	"DeadlineExceeded": true, // context.DeadlineExceeded
}

// checkErrorSentinelCmp detects direct ==/!= comparisons against sentinel
// errors where errors.Is() should be used. Returns warnings for newly-
// introduced patterns only (delta-aware).
func checkErrorSentinelCmp(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
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

	issues := findErrorSentinelCmpIssues(file, fset)
	if len(issues) == 0 {
		return nil
	}

	// Delta-aware: subtract pre-existing issues from old content.
	if strings.TrimSpace(oldContent) != "" {
		oldFset := token.NewFileSet()
		oldFile, oldErr := parser.ParseFile(oldFset, filePath, oldContent, 0)
		if oldErr == nil && oldFile != nil {
			oldSet := sentinelIssueSet(findErrorSentinelCmpIssues(oldFile, oldFset))
			filtered := issues[:0]
			for _, iss := range issues {
				if !oldSet[iss.key] {
					filtered = append(filtered, iss)
				}
			}
			issues = filtered
		}
	}

	if len(issues) == 0 {
		return nil
	}
	if len(issues) > maxErrorSentinelWarnings {
		issues = issues[:maxErrorSentinelWarnings]
	}
	warnings := make([]string, 0, len(issues))
	for _, iss := range issues {
		warnings = append(warnings, iss.message)
	}
	return warnings
}

// findErrorSentinelCmpIssues walks the AST and collects all error sentinel
// comparison anti-patterns.
func findErrorSentinelCmpIssues(file *ast.File, fset *token.FileSet) []errorSentinelIssue {
	var issues []errorSentinelIssue
	ast.Inspect(file, func(node ast.Node) bool {
		bin, ok := node.(*ast.BinaryExpr)
		if !ok {
			return true
		}
		if bin.Op != token.EQL && bin.Op != token.NEQ {
			return true
		}
		if !isErrorSentinelComparison(bin) {
			return true
		}
		pos := fset.Position(bin.Pos())
		issues = append(issues, errorSentinelIssue{
			key:     "sentinel-cmp:" + pos.String(),
			message: fmt.Sprintf(`Error sentinel comparison at %s: direct == or != on errors breaks when errors are wrapped (%%w). Use errors.Is(err, sentinel) instead to traverse the error chain, e.g. errors.Is(err, sql.ErrNoRows).`, pos.String()),
		})
		return true
	})
	return issues
}

// isErrorSentinelComparison returns true if a BinaryExpr compares an error
// variable to a sentinel error (excluding nil comparisons).
func isErrorSentinelComparison(bin *ast.BinaryExpr) bool {
	// Pattern: errVar == sentinel  or  sentinel == errVar
	if sentinelErrIsErrorVar(bin.X) && sentinelErrIsSentinel(bin.Y) {
		return true
	}
	if sentinelErrIsErrorVar(bin.Y) && sentinelErrIsSentinel(bin.X) {
		return true
	}
	return false
}

// sentinelErrIsErrorVar returns true for identifiers commonly used as error
// variables: "err", "e", or names ending in "err" (e.g., dbErr, parseErr).
func sentinelErrIsErrorVar(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	if !ok {
		return false
	}
	low := strings.ToLower(id.Name)
	if low == "err" || low == "e" {
		return true
	}
	return strings.HasSuffix(low, "err")
}

// sentinelErrIsSentinel returns true for expressions that reference a
// sentinel error value (package-level error variables, typically prefixed
// with "Err" or well-known stdlib sentinels like io.EOF).
func sentinelErrIsSentinel(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.SelectorExpr:
		return sentinelNameLooksLikeSentinel(v.Sel.Name)
	case *ast.Ident:
		return sentinelNameLooksLikeSentinel(v.Name)
	}
	return false
}

// sentinelNameLooksLikeSentinel returns true if a name matches common
// sentinel error naming conventions.
func sentinelNameLooksLikeSentinel(name string) bool {
	if strings.HasPrefix(name, "Err") {
		return true
	}
	return knownSentinelNames[name]
}

// sentinelIssueSet converts sentinel issues to a set keyed by issue.key.
func sentinelIssueSet(issues []errorSentinelIssue) map[string]bool {
	set := make(map[string]bool, len(issues))
	for _, iss := range issues {
		set[iss.key] = true
	}
	return set
}
