package agent

// SQL Injection Detection in Go Code
//
// Problem: AI coding agents frequently write SQL queries using string
// concatenation or fmt.Sprintf, creating SQL injection vulnerabilities
// (OWASP A03:2021 - Injection). The dangerous patterns are:
//
//   1. String concatenation in query:
//        db.Query("SELECT * FROM users WHERE name = '" + name + "'")
//
//   2. fmt.Sprintf interpolation:
//        db.Query(fmt.Sprintf("SELECT * FROM users WHERE role = '%s'", role))
//        db.Exec(fmt.Sprintf("DELETE FROM orders WHERE status = '%s'", status))
//
// Type-safe constructions are NOT flagged (issue #720): concatenation
// whose operands are all literals or strconv integer conversions, and
// Sprintf formats using only integer verbs (%d, %c, ...), produce digits or
// compile-time constants that cannot carry SQL metacharacters.
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
	"strconv"
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

		reason, advice := sqlInjCheckUnsafeArg(queryArg)
		if reason == "" {
			return true
		}

		// Fix #272: use the line where the query ARGUMENT starts, not the call
		// start. For multi-line calls the first line is just the generic
		// `rows, err := db.Query(` prefix, so a new vuln that changes the query
		// (SELECT -> DELETE) on a later line produced the same fingerprint as
		// the old one and was silently suppressed by the delta check. BinExpr
		// operands already start at the leftmost operand, so Pos() is the
		// start of the concatenation either way.
		lineNum := fset.Position(queryArg.Pos()).Line
		lineText := ""
		if lineNum >= 1 && lineNum <= len(lines) {
			lineText = strings.TrimSpace(lines[lineNum-1])
		}
		issues = append(issues, sqlInjIssue{
			text: fmt.Sprintf(
				"SQL injection risk at %s: %s %s",
				fset.Position(call.Pos()), reason, advice),
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
// reason string if it uses unsafe string concatenation or fmt.Sprintf
// interpolation, plus the advice text to append. Type-safe constructions
// are exempt (issue #720):
//
//   - Concatenation whose operands are ALL literals or safe integer-to-
//     string conversions (strconv.Itoa, strconv.FormatInt/FormatUint)
//     cannot inject: digits and compile-time constants carry no SQL
//     metacharacters.
//   - fmt.Sprintf whose format string only uses integer verbs (%d, %c, ...)
//     is silent for the same reason. String-capable verbs (%s, %v, %q) keep
//     the warning, softened to a recommendation because dynamic table and
//     column names are legitimately built via Sprintf and cannot be passed
//     as query parameters.
func sqlInjCheckUnsafeArg(arg ast.Expr) (reason, advice string) {
	switch expr := arg.(type) {
	case *ast.BinaryExpr:
		if expr.Op == token.ADD {
			if sqlInjConcatOperandsSafe(expr) {
				return "", ""
			}
			return "SQL query uses string concatenation (+).",
				"Use parameterized query with placeholders " +
					"(e.g., db.Query(\"SELECT ... WHERE col = ?\", value))."
		}
	case *ast.CallExpr:
		if sqlInjIsFmtSprintf(expr) {
			if sqlInjSprintfIntVerbsOnly(expr) {
				return "", ""
			}
			return "SQL query uses fmt.Sprintf with a string-capable verb (%s/%v/%q) for interpolation.",
				"If interpolated values may be user-controlled, consider a parameterized query with " +
					"placeholders; dynamic table or column names cannot be parameterized -- validate " +
					"them against an allowlist instead."
		}
	}
	return "", ""
}

// sqlInjConcatOperandsSafe reports whether every operand of a `+` chain is
// type-safe for SQL: numeric/string literals or integer-to-string
// conversions via strconv. Such operands can only produce digits or
// compile-time constant text, which cannot carry SQL metacharacters.
func sqlInjConcatOperandsSafe(expr *ast.BinaryExpr) bool {
	return sqlInjOperandSafe(expr.X) && sqlInjOperandSafe(expr.Y)
}

// sqlInjOperandSafe reports whether a single concatenation operand is
// provably injection-free. Bare identifiers, selector expressions, index
// expressions, and arbitrary calls have statically unknown types (they may
// be strings carrying user data), so they are conservatively unsafe.
func sqlInjOperandSafe(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.BasicLit:
		// INT and STRING literals are compile-time constants.
		return true
	case *ast.ParenExpr:
		return sqlInjOperandSafe(v.X)
	case *ast.BinaryExpr:
		return v.Op == token.ADD && sqlInjOperandSafe(v.X) && sqlInjOperandSafe(v.Y)
	case *ast.CallExpr:
		return sqlInjIsSafeIntToString(v)
	}
	return false
}

// sqlInjIsSafeIntToString reports whether the call is strconv.Itoa(x) or
// strconv.FormatInt/FormatUint(x, base) -- conversions whose output can
// only contain digits, a sign, or a base prefix.
func sqlInjIsSafeIntToString(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "strconv" {
		return false
	}
	switch sel.Sel.Name {
	case "Itoa":
		return len(call.Args) == 1
	case "FormatInt", "FormatUint":
		return len(call.Args) == 2
	}
	return false
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

// sqlInjSprintfIntVerbsOnly reports whether a fmt.Sprintf call's format
// string is a literal containing only integer-capable verbs (%d, %c, %b,
// %o, %O, %U, %x, %X). Those verbs render numbers as digit text and cannot
// embed SQL metacharacters, so the interpolation is type-safe. A non-literal
// format string is conservatively treated as unsafe.
func sqlInjSprintfIntVerbsOnly(call *ast.CallExpr) bool {
	if len(call.Args) == 0 {
		return false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	format, err := strconv.Unquote(lit.Value)
	if err != nil {
		return false
	}
	return sqlInjFormatIntVerbsOnly(format)
}

// sqlInjFormatIntVerbsOnly scans a format string and reports whether every
// verb is integer-capable. %% literals and flag/width/precision segments are
// skipped. String-capable verbs (%s, %v, %q) or any unknown verb fails.
func sqlInjFormatIntVerbsOnly(format string) bool {
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		if i+1 < len(format) && format[i+1] == '%' {
			i++ // %% is a literal percent sign
			continue
		}
		j := sqlInjSkipFormatFlags(format, i+1)
		if j >= len(format) {
			// Trailing '%' with no verb: Sprintf appends "%!(NOVERB)" and
			// embeds no argument value.
			return true
		}
		if !sqlInjIsIntVerb(format[j]) {
			// %s, %v, %q, %t, or anything else: value-dependent text.
			return false
		}
		i = j
	}
	return true
}

// sqlInjSkipFormatFlags returns the index of the first byte at or after
// start that is not part of a verb's flag/width/precision/argument-index
// segment (e.g. "%-08.3[2]d").
func sqlInjSkipFormatFlags(format string, start int) int {
	for j := start; j < len(format); j++ {
		c := format[j]
		isFlag := c == '+' || c == '-' || c == '#' || c == ' ' || c == '*' || c == '.' ||
			c == '[' || c == ']' || (c >= '0' && c <= '9')
		if !isFlag {
			return j
		}
	}
	return len(format)
}

// sqlInjIsIntVerb reports whether a format verb renders only integer values
// (digits, sign, or base prefix) and therefore cannot carry SQL metacharacters.
// %c is deliberately excluded (#1498): it renders the CODE POINT character
// itself, so a user-controlled 39 emits a quote that closes the string
// literal (45 '-', 59 ';' likewise) - it is the only integer verb that can
// produce non-digit text.
func sqlInjIsIntVerb(verb byte) bool {
	switch verb {
	case 'b', 'd', 'o', 'O', 'U', 'x', 'X':
		return true
	}
	return false
}
