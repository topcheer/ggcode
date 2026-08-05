package agent

// Time Format Layout Error Detection
//
// Trend: Multi-Language Knowledge Transfer Errors in AI Agents
//
// Problem: Go uses a unique reference-time-based date formatting system
// (Mon Jan 2 15:04:05 MST 2006) instead of the widely-used strftime/ISO-8601
// tokens (YYYY, MM, DD, %Y, %m, %d) found in Python, Java, JavaScript, and
// virtually every other language. AI coding agents trained on multi-language
// corpora frequently generate date layouts using the wrong token system.
//
// When an agent writes t.Format("YYYY-MM-DD"), Go outputs the literal string
// "YYYY-MM-DD" instead of a formatted date, because YYYY is not a valid Go
// time token. The correct tokens are 2006 (year), 01 (month), 02 (day), etc.
//
// This is NOT an i18n concern (i18n_check detects hardcoded-but-correct Go
// formats). This check detects INCORRECT layouts that produce silently wrong
// output at runtime.
//
// Competitor analysis:
//   - Claude Code: no detection
//   - Cursor: no detection (lint integrations do not flag this)
//   - Cline/OpenHands: no detection
//   - Aider: no detection
//   - go vet / staticcheck / golangci-lint: do not flag wrong layout tokens
//
// Only GoLand IDE has a basic inspection for this, behind a paywall.
// This check provides zero-dependency, AST-based detection at write time.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
)

// wrongLayoutTokensRe matches date format tokens from other languages that
// are NOT valid Go time layout tokens. If these appear in a Format/Parse
// layout string, the output will contain literal characters.
//
// Go's regexp engine (RE2) does not support lookbehind, so we use simple
// alternation patterns. False positives from valid Go tokens containing
// these substrings are mitigated by the fact that Go reference-time tokens
// (2006, 01, 02, 15, 04, 05) never contain YYYY/MM/DD/HH sequences.
//
// Patterns detected:
//   - YYYY/yyyy (4-digit year from Java/JS/Python date libraries)
//   - DD/dd (day of month, when surrounded by separators)
//   - HH/hh (hour)
//   - strftime: %Y %m %d %H %M %S %y %B %b %j
var wrongLayoutTokensRe = regexp.MustCompile(
	`(?i)YYYY|` + // 4-digit year (most common mistake)
		`(?i)MMM+|` + // month name abbreviation (Jan, January)
		`(?i)(^|[^A-Za-z0-9])MM($|[^A-Za-z0-9])|` + // 2-digit month as standalone
		`(?i)(^|[^A-Za-z0-9])DD($|[^A-Za-z])|` + // day of month
		`(?i)(^|[^A-Za-z0-9])dd($|[^A-Za-z0-9])|` + // lowercase day
		`(?i)HH|` + // hour
		`(?i)EEE+|` + // day of week
		`%[YmdHMSpyBbj]`, // strftime tokens
)

// goTimeFormatMethods identifies methods/functions that accept Go time layouts.
var goTimeFormatMethods = map[string]bool{
	"Format":          true,
	"Parse":           true,
	"ParseInLocation": true,
	"AppendFormat":    true,
}

// timeFormatIssue records a single wrong-layout occurrence.
type timeFormatIssue struct {
	method  string
	layout  string
	pos     token.Pos
	suggest string
}

// checkTimeFormat detects non-Go date format tokens in time.Format() and
// time.Parse() layout strings. Returns warning strings. Delta-aware: only
// flags patterns newly introduced by this edit.
func checkTimeFormat(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	newIssues := findWrongTimeLayouts(filePath, newContent)
	if len(newIssues) == 0 {
		return nil
	}

	// Delta: subtract patterns already present in old content.
	if strings.TrimSpace(oldContent) != "" {
		oldIssues := findWrongTimeLayouts(filePath, oldContent)
		if len(oldIssues) > 0 && len(oldIssues) >= len(newIssues) {
			return nil
		}
	}

	fset := token.NewFileSet()
	_, err := parser.ParseFile(fset, filePath, newContent, 0)
	if err != nil {
		return []string{fmt.Sprintf(
			"Detected %d time Format/Parse call(s) with non-Go layout tokens "+
				"in %s. Go uses reference time '2006-01-02 15:04:05' not "+
				"YYYY/MM/DD or strftime tokens.",
			len(newIssues), filepath.Base(filePath))}
	}

	var warnings []string
	for i, issue := range newIssues {
		if i >= 2 {
			break
		}
		suggestion := issue.suggest
		if suggestion == "" {
			suggestion = convertLayout(issue.layout)
		}
		warnings = append(warnings, fmt.Sprintf(
			"%s(%q) at %s:%d uses non-Go date tokens. Go does NOT use "+
				"YYYY/MM/DD or strftime (%%Y) tokens. The reference time is "+
				"'2006-01-02T15:04:05Z07:00'. Consider: %s(%q).",
			issue.method, issue.layout, filepath.Base(filePath),
			fset.Position(issue.pos).Line, issue.method, suggestion))
	}
	if extra := len(newIssues) - 2; extra > 0 {
		warnings = append(warnings,
			fmt.Sprintf("...and %d more wrong time layout(s) in %s",
				extra, filepath.Base(filePath)))
	}
	return warnings
}

// findWrongTimeLayouts parses Go source and returns all Format/Parse calls
// with non-Go layout tokens in their first string-literal argument.
func findWrongTimeLayouts(filename, src string) []timeFormatIssue {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil
	}

	var results []timeFormatIssue

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		methodName := extractTimeMethodName(call)
		if !goTimeFormatMethods[methodName] {
			return true
		}

		layout := extractLayoutString(call)
		if layout == "" {
			return true
		}

		if wrongLayoutTokensRe.MatchString(layout) {
			results = append(results, timeFormatIssue{
				method:  methodName,
				layout:  layout,
				pos:     call.Pos(),
				suggest: convertLayout(layout),
			})
		}
		return true
	})

	return results
}

// extractTimeMethodName returns the method name if the call is a Go time
// method, otherwise "".
func extractTimeMethodName(call *ast.CallExpr) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	return sel.Sel.Name
}

// extractLayoutString extracts the layout string argument from a time.Format
// or time.Parse call. The first argument is the layout for both.
func extractLayoutString(call *ast.CallExpr) string {
	if len(call.Args) == 0 {
		return ""
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	return unquoteGoString(lit.Value)
}

// convertLayout attempts to convert a non-Go date layout to the Go reference
// time equivalent. This is a best-effort heuristic conversion.
func convertLayout(layout string) string {
	repl := layout
	// Year tokens (order matters: longest first)
	repl = strings.ReplaceAll(repl, "YYYY", "2006")
	repl = strings.ReplaceAll(repl, "yyyy", "2006")
	repl = strings.ReplaceAll(repl, "YY", "06")
	repl = strings.ReplaceAll(repl, "yy", "06")
	// Month name tokens
	repl = strings.ReplaceAll(repl, "MMMM", "January")
	repl = strings.ReplaceAll(repl, "MMM", "Jan")
	// Month numeric
	repl = strings.ReplaceAll(repl, "MM", "01")
	// Day tokens
	repl = strings.ReplaceAll(repl, "DD", "02")
	repl = strings.ReplaceAll(repl, "dd", "02")
	// Hour tokens
	repl = strings.ReplaceAll(repl, "HH", "15")
	repl = strings.ReplaceAll(repl, "hh", "03")
	// Second tokens
	repl = strings.ReplaceAll(repl, "ss", "05")
	repl = strings.ReplaceAll(repl, "SS", "05")
	// strftime tokens
	repl = strings.ReplaceAll(repl, "%Y", "2006")
	repl = strings.ReplaceAll(repl, "%y", "06")
	repl = strings.ReplaceAll(repl, "%m", "01")
	repl = strings.ReplaceAll(repl, "%d", "02")
	repl = strings.ReplaceAll(repl, "%H", "15")
	repl = strings.ReplaceAll(repl, "%M", "04")
	repl = strings.ReplaceAll(repl, "%S", "05")
	return repl
}

// unquoteGoString removes surrounding quotes from a Go string literal value.
func unquoteGoString(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') ||
			(s[0] == '\'' && s[len(s)-1] == '\'') ||
			(s[0] == '`' && s[len(s)-1] == '`') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
