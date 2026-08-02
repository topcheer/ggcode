package agent

// Printf Format String Mismatch Detection in Go Code
//
// Problem: AI coding agents frequently produce Go code that passes a format
// string to a non-format function, or vice versa. The two classic bugs are:
//
//  1. Non-format string passed to a printf-family function:
//       log.Printf(userInput)        // BUG: if userInput contains %s, prints garbage
//       fmt.Println(fmt.Sprintf("%d items", n))  // BUG: Sprintf already formats
//       log.Fatal(err.Error())       // BUG: if err contains %, corrupted output
//
//  2. Format string passed to a non-format function (less severe but wasteful):
//       fmt.Println("%d items", n)   // BUG: prints literal "%d items n"
//
// These are compile-time errors in Go (go vet catches some), but many slip
// through because the arguments are strings and the compiler only enforces
// format-string checking for the built-in vet-known functions when vet runs.
// They cause runtime bugs: garbled log output, missing error messages, or
// misleading diagnostics that send the agent down a debugging rabbit hole.
//
// The most dangerous variant: passing a non-constant (variable) as the format
// argument to a printf-family function. If that variable happens to contain a
// percent sign (e.g. a file path like "/tmp/%s.log" or an error message), the
// output is silently corrupted or the program panics with "missing argument
// for verb".
//
// Competitor analysis:
//   - Claude Code: no inline detection (relies on go vet running separately)
//   - Cursor: lint-on-save may catch via go vet, but not at write time
//   - Cline/OpenHands: reactive only -- caught by tests or production incidents
//   - Aider: no automatic detection
//   - Windsurf: no automatic detection
//   - go vet: catches SOME cases (printf with wrong arg count) but only for
//     recognized function names and only with constant format strings; does NOT
//     flag fmt.Println(fmt.Sprintf(...)) redundancy or variable-as-format
//
// This check provides immediate, zero-dependency feedback at write time using
// Go's standard library AST parser. It catches three categories:
//
//  1. Non-constant format argument: first arg to Printf-family is not a string
//     literal (it's a variable, function call, or concatenation) -> injection risk.
//  2. Redundant Sprintf inside Println: fmt.Println(fmt.Sprintf(...)) ->
//     double-formatting, prints the format verbs literally.
//  3. Format verb count mismatch: for literal format strings, the number of %
//     verbs doesn't match the number of extra arguments.
//
// Delta-aware: only flags patterns newly introduced by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// printfFamily maps printf-family functions to whether they are variadic in
// the format-args sense. These are the functions where the FIRST argument is
// the format string and subsequent arguments are substituted into format verbs.
var printfFamily = map[string]bool{
	"fmt.Sprintf":  true,
	"fmt.Errorf":   true,
	"fmt.Printf":   true,
	"fmt.Fprintf":  true, // first arg is io.Writer, second is format
	"fmt.Sprintln": false,
	"log.Printf":   true,
	"log.Fatalf":   true,
	"log.Panicf":   true,
}

// nonFormatPrintFamily are print functions that should NOT receive a format
// string as a direct argument (they print verbatim). If their first argument
// is a call to a printf-family function, it's likely a double-formatting bug.
var nonFormatPrintFamily = map[string]bool{
	"fmt.Print":    true,
	"fmt.Println":  true,
	"fmt.Fprint":   true,
	"fmt.Fprintln": true,
	"log.Print":    true,
	"log.Println":  true,
	"log.Fatal":    true,
	"log.Fatalln":  true,
	"log.Panic":    true,
	"log.Panicln":  true,
}

// printfFormatInfo records a single printf format mismatch with location.
type printfFormatInfo struct {
	kind     string // "nonconstant-format", "redundant-sprintf", "verb-count"
	funcName string // e.g., "log.Printf"
	line     int    // 1-based line number
	detail   string // human-readable detail for the warning
}

// checkPrintfFormat detects printf format string mismatches in Go code.
// Returns warning strings. Only flags NEW occurrences (delta-aware).
//
// Parameters:
//   - filePath: path of the written file (used for language detection)
//   - oldContent: the file content before the write ("" for new files)
//   - newContent: the file content after the write
func checkPrintfFormat(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	newIssues := findPrintfFormatIssues(newContent)
	if len(newIssues) == 0 {
		return nil
	}

	// Delta-aware: count old issues and subtract.
	if strings.TrimSpace(oldContent) != "" {
		oldCount := len(findPrintfFormatIssues(oldContent))
		if len(newIssues) <= oldCount {
			return nil
		}
		// Report only the newly-introduced issues (the surplus).
		newIssues = newIssues[oldCount:]
	}

	if len(newIssues) == 0 {
		return nil
	}

	// Group by kind for concise reporting.
	kindCount := make(map[string]int)
	for _, iss := range newIssues {
		kindCount[iss.kind]++
	}

	var warnings []string
	for _, iss := range newIssues {
		switch iss.kind {
		case "nonconstant-format":
			warnings = append(warnings, fmt.Sprintf(
				"`%s` (line %d) is called with a non-constant format string (%s). "+
					"If the value contains a `%%` verb, the output is corrupted or the program panics "+
					"with a missing-argument error. Use `%s(\"%%s\", %s)` or a non-format function like "+
					"Print/Println instead.",
				iss.funcName, iss.line, iss.detail, iss.funcName, iss.detail))
		case "redundant-sprintf":
			warnings = append(warnings, fmt.Sprintf(
				"`%s(fmt.Sprintf(...))` (line %d) double-formats the string. Sprintf already "+
					"substitutes format verbs, so Println prints the result verbatim (including any "+
					"literal `%%` from the original). Remove the Sprintf wrapper, or replace Println "+
					"with Printf if you need format substitution.",
				iss.funcName, iss.line))
		case "verb-count":
			warnings = append(warnings, fmt.Sprintf(
				"`%s` (line %d) format string has %s. This causes a go vet error or garbled output. "+
					"Ensure the number of `%%` verbs matches the number of arguments passed after the format string.",
				iss.funcName, iss.line, iss.detail))
		}
	}

	return warnings
}

// findPrintfFormatIssues parses Go source and returns all printf format
// mismatches found, ordered by position.
func findPrintfFormatIssues(src string) []printfFormatInfo {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil || file == nil {
		return nil
	}

	var results []printfFormatInfo

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		name := qualifiedCallName(call)
		if name == "" {
			return true
		}

		pos := fset.Position(call.Pos())

		// Check printf-family: first format-arg position depends on function.
		// For Fprintf/Fprint, the first arg is the writer, format is second.
		if printfFamily[name] {
			formatArgIdx := 0
			if name == "fmt.Fprintf" {
				formatArgIdx = 1
			}
			if issue := analyzePrintfCall(name, call, formatArgIdx, pos.Line); issue != nil {
				results = append(results, *issue)
			}
		}

		// Check non-format print family for redundant Sprintf wrapping.
		if nonFormatPrintFamily[name] {
			if issue := analyzeNonFormatCall(name, call, pos.Line); issue != nil {
				results = append(results, *issue)
			}
		}

		return true
	})

	return results
}

// analyzePrintfCall checks a printf-family call for format string issues.
func analyzePrintfCall(funcName string, call *ast.CallExpr, formatArgIdx, line int) *printfFormatInfo {
	args := call.Args
	if len(args) <= formatArgIdx {
		return nil
	}

	formatArg := args[formatArgIdx]

	// Case 1: Non-constant format string.
	// The format arg is a variable, function call (other than a safe literal
	// builder), binary expression, etc. String literals are safe.
	if !isStringLiteral(formatArg) {
		// fmt.Errorf is often called with err.Error() or string concatenation
		// for wrapping; allow that common pattern to reduce false positives.
		if funcName == "fmt.Errorf" && isErrErrorCall(formatArg) {
			return nil
		}
		detail := formatExprToString(formatArg)
		// Skip if the expression is a bare identifier that is likely a constant
		// (we can't tell for sure without type info, but skip common cases).
		if isLikelyConstant(formatArg) {
			return nil
		}
		return &printfFormatInfo{
			kind:     "nonconstant-format",
			funcName: funcName,
			line:     line,
			detail:   detail,
		}
	}

	// Case 2: For literal format strings, check verb count vs argument count.
	lit, ok := formatArg.(*ast.BasicLit)
	if !ok {
		return nil
	}
	formatStr, ok := unquoteBasicLit(lit.Value)
	if !ok {
		return nil
	}

	verbs := countFormatVerbs(formatStr)
	extraArgs := len(args) - formatArgIdx - 1

	if verbs != extraArgs {
		return &printfFormatInfo{
			kind:     "verb-count",
			funcName: funcName,
			line:     line,
			detail:   fmt.Sprintf("%d format verb(s) but %d argument(s)", verbs, extraArgs),
		}
	}

	return nil
}

// analyzeNonFormatCall checks a non-format print call (Println, Print) for
// redundant fmt.Sprintf wrapping.
func analyzeNonFormatCall(funcName string, call *ast.CallExpr, line int) *printfFormatInfo {
	if len(call.Args) == 0 {
		return nil
	}

	firstArg := call.Args[0]
	innerName := callNameFromExpr(firstArg)

	if printfFamily[innerName] && innerName == "fmt.Sprintf" {
		return &printfFormatInfo{
			kind:     "redundant-sprintf",
			funcName: funcName,
			line:     line,
		}
	}
	return nil
}

// isStringLiteral returns true if the expression is a Go string literal
// (BasicLit of kind STRING).
func isStringLiteral(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Kind == token.STRING
}

// isErrErrorCall returns true if the expression looks like `err.Error()`.
func isErrErrorCall(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return sel.Sel != nil && sel.Sel.Name == "Error"
}

// isLikelyConstant returns true for expressions that are very likely compile-
// time constants and thus safe as format strings. This reduces false positives
// for patterns like: fmt.Printf(myTemplate) where myTemplate is a const.
func isLikelyConstant(e ast.Expr) bool {
	// Identifiers starting with uppercase or all-caps are likely exported
	// constants. This is heuristic and conservative.
	if ident, ok := e.(*ast.Ident); ok {
		name := ident.Name
		if len(name) == 0 {
			return false
		}
		// All-caps identifiers (e.g. FORMAT, TEMPLATE) are likely constants.
		isAllUpper := true
		hasLetter := false
		for _, c := range name {
			if c >= 'a' && c <= 'z' {
				isAllUpper = false
			}
			if c >= 'A' && c <= 'Z' {
				hasLetter = true
			}
		}
		return isAllUpper && hasLetter
	}
	return false
}

// exprToString renders an AST expression to a short string for display.
func formatExprToString(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.CallExpr:
		return callNameFromExpr(e) + "(...)"
	case *ast.SelectorExpr:
		return formatExprToString(v.X) + "." + v.Sel.Name
	case *ast.BinaryExpr:
		return formatExprToString(v.X) + " " + v.Op.String() + " " + formatExprToString(v.Y)
	default:
		return "<expr>"
	}
}

// callNameFromExpr extracts the qualified name from a call expression,
// or "" if the expression is not a call.
func callNameFromExpr(e ast.Expr) string {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return ""
	}
	return qualifiedCallName(call)
}

// countFormatVerbs counts the number of format verbs (%s, %d, %v, %%, etc.)
// in a printf format string. Returns the count of substitution verbs (excluding
// literal %% which does not consume an argument).
func countFormatVerbs(format string) int {
	count := 0
	i := 0
	for i < len(format) {
		if format[i] != '%' {
			i++
			continue
		}
		i++
		if i >= len(format) {
			break
		}
		// %% is a literal percent, not a verb.
		if i < len(format) && format[i] == '%' {
			i++
			continue
		}
		// Skip explicit argument index: %[N]
		if i < len(format) && format[i] == '[' {
			i++
			for i < len(format) && isDigit(format[i]) {
				i++
			}
			if i < len(format) && format[i] == ']' {
				i++
			}
		}
		// Skip over flags, width, precision, '*'.
		for i < len(format) && (isFlagChar(format[i]) || format[i] == '*' || isDigit(format[i])) {
			i++
		}
		// Skip precision dot and its digits.
		if i < len(format) && format[i] == '.' {
			i++
			for i < len(format) && (format[i] == '*' || isDigit(format[i])) {
				i++
			}
		}
		// The next non-flag character is the verb.
		if i < len(format) && isVerbChar(format[i]) {
			count++
			i++
		}
	}
	return count
}

func isFlagChar(c byte) bool {
	return c == '+' || c == '-' || c == '#' || c == ' ' || c == '0'
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func isVerbChar(c byte) bool {
	// Common printf verbs in Go.
	switch c {
	case 'v', 'd', 's', 'q', 'x', 'X', 't', 'T', 'b', 'c', 'o', 'O', 'U', 'e',
		'E', 'f', 'F', 'g', 'G', 'p', 'w':
		return true
	}
	return false
}

// unquoteBasicLit removes surrounding quotes from a Go string literal value.
// Returns the unquoted string and true on success.
func unquoteBasicLit(s string) (string, bool) {
	if len(s) < 2 {
		return "", false
	}
	// Handle backtick strings.
	if s[0] == '`' && s[len(s)-1] == '`' {
		return s[1 : len(s)-1], true
	}
	// Handle double-quote strings (basic unescape, good enough for verb counting).
	if s[0] == '"' && s[len(s)-1] == '"' {
		inner := s[1 : len(s)-1]
		return inner, true
	}
	return "", false
}
