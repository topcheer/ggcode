package agent

// SQL Injection Detection in Go Code
//
// Problem: AI coding agents frequently write SQL queries using string
// concatenation or fmt.Sprintf, creating SQL injection vulnerabilities
// (OWASP A03:2021 - Injection). The dangerous patterns are:
//
//   1. String concatenation in query:
//        db.Query("SELECT * FROM users WHERE name = '" + name + "'")
//        rows, _ := db.Query("SELECT * FROM t WHERE id = " + strconv.Itoa(id))
//
//   2. fmt.Sprintf interpolation:
//        db.Query(fmt.Sprintf("SELECT * FROM users WHERE role = '%s'", role))
//        db.Exec(fmt.Sprintf("DELETE FROM orders WHERE status = '%s'", status))
//
// The safe alternative uses parameterized queries with placeholders (? or $1):
//   db.Query("SELECT * FROM users WHERE name = ?", name)
//   db.QueryRow("SELECT * FROM users WHERE id = $1", id)
//
// These vulnerabilities pass compilation and basic testing but are exploitable
// at runtime. No AI coding agent detects this at write time.
//
// Competitor analysis:
//   - Claude Code: no write-time detection (relies on gosec/semgrep)
//   - Cursor: no detection (relies on external linters)
//   - Cline/OpenHands: no detection
//   - gosec G201/G202: detects at lint time, not write time
//   - semgrep: detects at CI time, not write time
//
// Approach: AST-based analysis of Go files. For each call to a database
// method (Query, Exec, QueryRow, Prepare, etc.), check if the query argument
// is built via string concatenation or fmt.Sprintf. Zero LLM cost, pure AST
// pattern matching. Delta-aware: only flags patterns newly introduced.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

const maxSQLInjectionWarnings = 4

// sqlInjMethods are database/sql and sqlx methods that take a SQL query
// string as their first or second (after context) argument. Get and Select
// (sqlx) and MustExec are deliberately EXCLUDED: without receiver checking,
// they fire on common redis/cache/map idioms like cache.Get(fmt.Sprintf(...))
// or m.Select("prefix-"+key).
var sqlInjMethods = map[string]bool{
	"Query":           true,
	"QueryContext":    true,
	"QueryRow":        true,
	"QueryRowContext": true,
	"Exec":            true,
	"ExecContext":     true,
	"Prepare":         true,
	"PrepareContext":  true,
	"NamedExec":       true,
	"NamedQuery":      true,
}

// sqlInjContextMethods require a context.Context as the first argument,
// so the query string is the second argument.
var sqlInjContextMethods = map[string]bool{
	"QueryContext":    true,
	"QueryRowContext": true,
	"ExecContext":     true,
	"PrepareContext":  true,
}

// checkSQLInjection detects SQL queries built via string concatenation or
// fmt.Sprintf, flagging potential SQL injection vulnerabilities. Delta-aware:
// instances whose fingerprint (method + trimmed line text) already exists in
// oldContent are suppressed, so pre-existing risky queries are not re-reported
// on every edit.
func checkSQLInjection(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	newIssues := sqlInjScan(filePath, newContent)
	if newIssues == nil {
		return nil
	}

	// Delta suppression: drop instances that already existed in old content.
	if strings.TrimSpace(oldContent) != "" {
		oldSet := sqlInjScanFingerprints(filePath, oldContent)
		kept := newIssues[:0]
		for _, ni := range newIssues {
			if !oldSet[ni.fingerprint] {
				kept = append(kept, ni)
			}
		}
		newIssues = kept
	}

	var warnings []string
	for _, ni := range newIssues {
		if len(warnings) >= maxSQLInjectionWarnings {
			warnings = append(warnings, "...and possibly more SQL injection risks (capped)")
			break
		}
		warnings = append(warnings, ni.text)
	}
	return warnings
}

// sqlInjIssue is one SQL injection finding with its delta fingerprint.
type sqlInjIssue struct {
	text        string
	fingerprint string
}

// sqlInjScan parses the content and returns all unsafe-query findings.
func sqlInjScan(filePath, content string) []sqlInjIssue {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, content, 0)
	if err != nil || file == nil {
		return nil
	}
	lines := strings.Split(content, "\n")

	var issues []sqlInjIssue
	ast.Inspect(file, func(node ast.Node) bool {
		if len(issues) >= maxSQLInjectionWarnings+1 {
			return false
		}

		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		methodName := sqlInjExtractMethodName(call)
		if methodName == "" || !sqlInjMethods[methodName] {
			return true
		}

		queryArg := sqlInjGetQueryArg(call, methodName)
		if queryArg == nil {
			return true
		}

		reason := sqlInjCheckUnsafeArg(queryArg)
		if reason == "" {
			return true
		}

		lineNum := fset.Position(call.Pos()).Line
		lineText := ""
		if lineNum >= 1 && lineNum <= len(lines) {
			lineText = strings.TrimSpace(lines[lineNum-1])
		}
		issues = append(issues, sqlInjIssue{
			text: fmt.Sprintf(
				"SQL injection risk at %s: %s Use parameterized query with placeholders "+
					"(e.g., db.Query(\"SELECT ... WHERE col = ?\", value)).",
				fset.Position(call.Pos()), reason),
			fingerprint: methodName + "|" + lineText,
		})
		return true
	})
	return issues
}

// sqlInjScanFingerprints returns the set of delta fingerprints for a content.
func sqlInjScanFingerprints(filePath, content string) map[string]bool {
	issues := sqlInjScan(filePath, content)
	set := make(map[string]bool, len(issues))
	for _, i := range issues {
		set[i.fingerprint] = true
	}
	return set
}

// sqlInjExtractMethodName returns the method name from a call expression
// if it is a selector expression (e.g., db.Query -> "Query").
func sqlInjExtractMethodName(call *ast.CallExpr) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	return sel.Sel.Name
}

// sqlInjGetQueryArg returns the AST node for the SQL query argument.
// For context-variant methods, the query is the second argument (index 1).
// For others, it is the first argument (index 0).
func sqlInjGetQueryArg(call *ast.CallExpr, methodName string) ast.Expr {
	argIdx := 0
	if sqlInjContextMethods[methodName] {
		argIdx = 1
	}
	if len(call.Args) <= argIdx {
		return nil
	}
	return call.Args[argIdx]
}

// sqlInjCheckUnsafeArg inspects a query argument and returns a non-empty
// reason string if it uses string concatenation or fmt.Sprintf.
func sqlInjCheckUnsafeArg(arg ast.Expr) string {
	switch expr := arg.(type) {
	case *ast.BinaryExpr:
		if expr.Op == token.ADD {
			return "SQL query uses string concatenation (+)."
		}
	case *ast.CallExpr:
		if sqlInjIsFmtSprintf(expr) {
			return "SQL query uses fmt.Sprintf for interpolation."
		}
	}
	return ""
}

// sqlInjIsFmtSprintf returns true if the call expression is fmt.Sprintf.
func sqlInjIsFmtSprintf(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return pkg.Name == "fmt" && sel.Sel.Name == "Sprintf"
}
