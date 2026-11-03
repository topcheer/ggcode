package agent

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
)

const maxSQLInjectionWarnings = 4

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

var sqlInjContextMethods = map[string]bool{
	"QueryContext":    true,
	"QueryRowContext": true,
	"ExecContext":     true,
	"PrepareContext":  true,
}

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

type sqlInjIssue struct {
	text        string
	fingerprint string
}

func sqlInjScan(filePath, content string) []sqlInjIssue {
	return sqlInjScanCapped(filePath, content, true)
}

func sqlInjScanAll(filePath, content string) []sqlInjIssue {
	return sqlInjScanCapped(filePath, content, false)
}

func sqlInjScanCapped(filePath, content string, capped bool) []sqlInjIssue {
	file, fset, err := parseGoSource(filePath, content, 0)
	if err != nil || file == nil {
		return nil
	}
	lines := strings.Split(content, "\n")

	var issues []sqlInjIssue
	ast.Inspect(file, func(node ast.Node) bool {
		if capped && len(issues) >= maxSQLInjectionWarnings+1 {
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

func sqlInjScanFingerprints(filePath, content string) map[string]bool {
	issues := sqlInjScanAll(filePath, content)
	set := make(map[string]bool, len(issues))
	for _, i := range issues {
		set[i.fingerprint] = true
	}
	return set
}

func sqlInjExtractMethodName(call *ast.CallExpr) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	return sel.Sel.Name
}

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

func sqlInjConcatOperandsSafe(expr *ast.BinaryExpr) bool {
	return sqlInjOperandSafe(expr.X) && sqlInjOperandSafe(expr.Y)
}

func sqlInjOperandSafe(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.BasicLit:

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

func sqlInjFormatIntVerbsOnly(format string) bool {
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		if i+1 < len(format) && format[i+1] == '%' {
			i++
			continue
		}
		j := sqlInjSkipFormatFlags(format, i+1)
		if j >= len(format) {

			return true
		}
		if !sqlInjIsIntVerb(format[j]) {

			return false
		}
		i = j
	}
	return true
}

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

func sqlInjIsIntVerb(verb byte) bool {
	switch verb {
	case 'b', 'd', 'o', 'O', 'U', 'x', 'X':
		return true
	}
	return false
}
