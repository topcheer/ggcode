package agent

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"
)

var printfFamily = map[string]bool{
	"fmt.Sprintf":  true,
	"fmt.Errorf":   true,
	"fmt.Printf":   true,
	"fmt.Fprintf":  true,
	"fmt.Sprintln": false,
	"log.Printf":   true,
	"log.Fatalf":   true,
	"log.Panicf":   true,
}

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

type printfFormatInfo struct {
	kind     string
	funcName string
	line     int
	detail   string
}

func checkPrintfFormat(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	newIssues := filterPreexistingPrintfIssues(oldContent, findPrintfFormatIssues(newContent))
	if len(newIssues) == 0 {
		return nil
	}

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

func filterPreexistingPrintfIssues(oldContent string, newIssues []printfFormatInfo) []printfFormatInfo {
	if strings.TrimSpace(oldContent) == "" || len(newIssues) == 0 {
		return newIssues
	}
	oldIssues := findPrintfFormatIssues(oldContent)
	oldCounts := make(map[string]int, len(oldIssues))
	for _, iss := range oldIssues {
		oldCounts[iss.kind+"\x00"+iss.funcName+"\x00"+iss.detail]++
	}
	consumed := make(map[string]int, len(oldCounts))
	var fresh []printfFormatInfo
	for _, iss := range newIssues {
		key := iss.kind + "\x00" + iss.funcName + "\x00" + iss.detail
		consumed[key]++
		if consumed[key] <= oldCounts[key] {
			continue
		}
		fresh = append(fresh, iss)
	}
	return fresh
}

func findPrintfFormatIssues(src string) []printfFormatInfo {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	file, fset, err := parseGoSource("", src, 0)
	if err != nil || file == nil {
		return nil
	}

	var results []printfFormatInfo
	ast.Walk(&printfFormatVisitor{fset: fset, issues: &results}, file)
	return results
}

type printfFormatVisitor struct {
	fset   *token.FileSet
	issues *[]printfFormatInfo
	params map[string]bool
}

func (v *printfFormatVisitor) Visit(n ast.Node) ast.Visitor {
	switch fn := n.(type) {
	case *ast.FuncDecl:
		return &printfFormatVisitor{fset: v.fset, issues: v.issues, params: funcParamNames(fn.Type.Params)}
	case *ast.FuncLit:

		merged := make(map[string]bool, len(v.params)+4)
		for k := range v.params {
			merged[k] = true
		}
		for k := range funcParamNames(fn.Type.Params) {
			merged[k] = true
		}
		return &printfFormatVisitor{fset: v.fset, issues: v.issues, params: merged}
	case *ast.CallExpr:
		v.checkCall(fn)
	}
	return v
}

func (v *printfFormatVisitor) checkCall(call *ast.CallExpr) {
	name := qualifiedCallName(call)
	if name == "" {
		return
	}
	pos := v.fset.Position(call.Pos())

	if printfFamily[name] {
		formatArgIdx := 0
		if name == "fmt.Fprintf" {
			formatArgIdx = 1
		}
		if issue := analyzePrintfCall(name, call, formatArgIdx, pos.Line, v.params); issue != nil {
			*v.issues = append(*v.issues, *issue)
		}
	}

	if nonFormatPrintFamily[name] {
		if issue := analyzeNonFormatCall(name, call, pos.Line); issue != nil {
			*v.issues = append(*v.issues, *issue)
		}
	}
}

func funcParamNames(fl *ast.FieldList) map[string]bool {
	if fl == nil {
		return nil
	}
	out := make(map[string]bool)
	for _, field := range fl.List {
		for _, name := range field.Names {
			out[name.Name] = true
		}
	}
	return out
}

func analyzePrintfCall(funcName string, call *ast.CallExpr, formatArgIdx, line int, params map[string]bool) *printfFormatInfo {
	args := call.Args
	if len(args) <= formatArgIdx {
		return nil
	}

	formatArg := args[formatArgIdx]

	if !isStringConstExpr(formatArg) {

		if funcName == "fmt.Errorf" && isErrErrorCall(formatArg) {
			return nil
		}

		if isForwardedFormatExpr(formatArg, params) && endsWithVariadicParam(call, params) {
			return nil
		}
		detail := formatExprToString(formatArg)

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

	formatStr, ok := constFormatString(formatArg)
	if !ok {
		return nil
	}

	if call.Ellipsis.IsValid() {
		return nil
	}

	if usesExplicitIndex(formatStr) {
		return nil
	}

	argsNeeded := countFormatArgs(formatStr)
	extraArgs := len(args) - formatArgIdx - 1

	if argsNeeded != extraArgs {
		return &printfFormatInfo{
			kind:     "verb-count",
			funcName: funcName,
			line:     line,
			detail:   fmt.Sprintf("%d format verb(s) but %d argument(s)", argsNeeded, extraArgs),
		}
	}

	return nil
}

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

func isLikelyConstant(e ast.Expr) bool {

	if ident, ok := e.(*ast.Ident); ok {
		name := ident.Name
		if len(name) == 0 {
			return false
		}

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

func callNameFromExpr(e ast.Expr) string {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return ""
	}
	return qualifiedCallName(call)
}

func isForwardedFormatExpr(e ast.Expr, params map[string]bool) bool {
	if len(params) == 0 {
		return false
	}
	switch v := e.(type) {
	case *ast.Ident:
		return params[v.Name]
	case *ast.BinaryExpr:
		return v.Op == token.ADD &&
			isStringConstExpr(v.X) &&
			isForwardedFormatExpr(v.Y, params)
	}
	return false
}

func isStringConstExpr(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.BasicLit:
		return v.Kind == token.STRING
	case *ast.BinaryExpr:
		return v.Op == token.ADD && isStringConstExpr(v.X) && isStringConstExpr(v.Y)
	}
	return false
}

func endsWithVariadicParam(call *ast.CallExpr, params map[string]bool) bool {
	if !call.Ellipsis.IsValid() || len(call.Args) == 0 || len(params) == 0 {
		return false
	}
	id, ok := call.Args[len(call.Args)-1].(*ast.Ident)
	return ok && params[id.Name]
}

func constFormatString(e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		return unquoteBasicLit(v.Value)
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		x, ok := constFormatString(v.X)
		if !ok {
			return "", false
		}
		y, ok := constFormatString(v.Y)
		if !ok {
			return "", false
		}
		return x + y, true
	}
	return "", false
}

func usesExplicitIndex(format string) bool {
	return strings.Contains(format, "%[")
}

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

		if format[i] == '%' {
			i++
			continue
		}

		if verb, _, ok := parseFormatDirective(format, i); ok {
			count++
			i = verb
		} else {
			i++
		}
	}
	return count
}

func countFormatArgs(format string) int {
	args := 0
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
		if format[i] == '%' {
			i++
			continue
		}
		if next, stars, ok := parseFormatDirective(format, i); ok {
			args += 1 + stars
			i = next
		} else {
			i++
		}
	}
	return args
}

func parseFormatDirective(format string, start int) (int, int, bool) {
	i := start

	if i < len(format) && format[i] == '[' {
		i++
		for i < len(format) && isDigit(format[i]) {
			i++
		}
		if i < len(format) && format[i] == ']' {
			i++
		}
	}

	region := i
	i = skipWhile(format, i, isFlagWidthChar)
	if i < len(format) && format[i] == '.' {
		i++
		i = skipWhile(format, i, isPrecChar)
	}

	if i < len(format) && isVerbChar(format[i]) {
		return i + 1, strings.Count(format[region:i], "*"), true
	}
	return start, 0, false
}

func isFlagWidthChar(b byte) bool {
	return isFlagChar(b) || isDigit(b) || b == '*'
}

func isPrecChar(b byte) bool {
	return isDigit(b) || b == '*'
}

func skipWhile(s string, i int, pred func(byte) bool) int {
	for i < len(s) && pred(s[i]) {
		i++
	}
	return i
}

func isFlagChar(c byte) bool {
	return c == '+' || c == '-' || c == '#' || c == ' ' || c == '0'
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func isVerbChar(c byte) bool {

	switch c {
	case 'v', 'd', 's', 'q', 'x', 'X', 't', 'T', 'b', 'c', 'o', 'O', 'U', 'e',
		'E', 'f', 'F', 'g', 'G', 'p', 'w':
		return true
	}
	return false
}

func unquoteBasicLit(s string) (string, bool) {
	if len(s) < 2 {
		return "", false
	}

	if s[0] == '`' && s[len(s)-1] == '`' {
		return s[1 : len(s)-1], true
	}

	if s[0] == '"' && s[len(s)-1] == '"' {
		inner := s[1 : len(s)-1]
		return inner, true
	}
	return "", false
}
