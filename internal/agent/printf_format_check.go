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

	newIssues := filterPreexistingPrintfIssues(oldContent, findPrintfFormatIssues(newContent))
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

// filterPreexistingPrintfIssues drops issues that already existed in the old
// content, via per-instance multiset comparison keyed by
// kind+funcName+detail (fix #533, the #186/#171 per-instance multiset
// idiom): the LINE NUMBER is deliberately excluded so an unrelated edit
// that shifts a pre-existing issue to another line does not re-report it.
// `detail` carries the instance's stable content (the variable name for
// nonconstant-format, the verb-vs-arg counts for verb-count), which keeps
// the #172 fix intact: removing one instance while adding a different one
// still yields a multiset surplus and is reported.
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
			continue // pre-existing instance; its multiset slot is already filled
		}
		fresh = append(fresh, iss)
	}
	return fresh
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
	ast.Walk(&printfFormatVisitor{fset: fset, issues: &results}, file)
	return results
}

// printfFormatVisitor walks the AST carrying the enclosing function's
// parameter names so calls are judged in scope (#505): a printf-family call
// whose format argument IS a parameter of the enclosing function and whose
// variadic tail spreads another parameter is idiomatic forwarding, not an
// injection risk.
type printfFormatVisitor struct {
	fset   *token.FileSet
	issues *[]printfFormatInfo
	params map[string]bool // parameter names of the enclosing func; nil at top level
}

// Visit implements ast.Visitor. Returning a fresh visitor scopes parameter
// tracking to that function's subtree.
func (v *printfFormatVisitor) Visit(n ast.Node) ast.Visitor {
	switch fn := n.(type) {
	case *ast.FuncDecl:
		return &printfFormatVisitor{fset: v.fset, issues: v.issues, params: funcParamNames(fn.Type.Params)}
	case *ast.FuncLit:
		// Closures also see the enclosing scope's names.
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

// checkCall judges one call expression against the printf families.
func (v *printfFormatVisitor) checkCall(call *ast.CallExpr) {
	name := qualifiedCallName(call)
	if name == "" {
		return
	}
	pos := v.fset.Position(call.Pos())

	// printf-family: first format-arg position depends on function.
	// For Fprintf/Fprint, the first arg is the writer, format is second.
	if printfFamily[name] {
		formatArgIdx := 0
		if name == "fmt.Fprintf" {
			formatArgIdx = 1
		}
		if issue := analyzePrintfCall(name, call, formatArgIdx, pos.Line, v.params); issue != nil {
			*v.issues = append(*v.issues, *issue)
		}
	}

	// non-format print family: redundant Sprintf wrapping.
	if nonFormatPrintFamily[name] {
		if issue := analyzeNonFormatCall(name, call, pos.Line); issue != nil {
			*v.issues = append(*v.issues, *issue)
		}
	}
}

// funcParamNames returns the declared parameter names as a set.
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

// analyzePrintfCall checks a printf-family call for format string issues.
func analyzePrintfCall(funcName string, call *ast.CallExpr, formatArgIdx, line int, params map[string]bool) *printfFormatInfo {
	args := call.Args
	if len(args) <= formatArgIdx {
		return nil
	}

	formatArg := args[formatArgIdx]

	// Case 1: Non-constant format string.
	// The format arg is a variable, function call (other than a safe literal
	// builder), binary expression, etc. String literals — including
	// compile-time concatenations of literals ("a "+"%d", #533) — are safe.
	if !isStringConstExpr(formatArg) {
		// fmt.Errorf is often called with err.Error() or string concatenation
		// for wrapping; allow that common pattern to reduce false positives.
		if funcName == "fmt.Errorf" && isErrErrorCall(formatArg) {
			return nil
		}
		// #505: idiomatic forwarding — the enclosing function received the
		// format (and the variadic args it spreads) as its own parameters:
		//   func logf(format string, args ...any) { log.Printf(format, args...) }
		// This is Go's most common wrapper shape. go vet's printf checker
		// recognizes it via type information and checks the wrapper's callers
		// instead of flagging the forwarding line itself; without types we
		// approximate by scope: format is a parameter AND the call ends with
		// a spread of another parameter.
		if isForwardedFormatExpr(formatArg, params) && endsWithVariadicParam(call, params) {
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

	// Case 2: For constant format strings (literal or literal concatenation,
	// #533), check verb count vs argument count.
	formatStr, ok := constFormatString(formatArg)
	if !ok {
		return nil
	}

	// #505: variadic spread — the runtime slice length is unknowable, so
	// counting the spread as exactly one argument is wrong. go vet skips
	// spread calls for the same reason (fmt.Sprintf("%s=%v\n", kv...) is
	// correct when kv has two elements).
	if call.Ellipsis.IsValid() {
		return nil
	}
	// #505: explicit argument indexes (%[1]s) reuse arguments; naive verb
	// counting is invalid ("%[1]s and %[1]s" with one arg is correct).
	if usesExplicitIndex(formatStr) {
		return nil
	}

	// #533: each `*` width/precision consumes an extra argument, so the
	// argument count (not the bare verb count) must match.
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

// --- #505: variadic-forwarding and spread exemptions ---

// isForwardedFormatExpr reports whether expr is a forwarding-safe format
// argument for a call inside the function whose parameters are params: a
// bare parameter identifier, or a string-literal prefix concatenated onto
// one ("[WARN] "+format).
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

// isStringConstExpr reports whether expr is a string literal or a
// concatenation of string literals.
func isStringConstExpr(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.BasicLit:
		return v.Kind == token.STRING
	case *ast.BinaryExpr:
		return v.Op == token.ADD && isStringConstExpr(v.X) && isStringConstExpr(v.Y)
	}
	return false
}

// endsWithVariadicParam reports whether the call's final argument is a
// spread (x...) of a parameter of the enclosing function.
func endsWithVariadicParam(call *ast.CallExpr, params map[string]bool) bool {
	if !call.Ellipsis.IsValid() || len(call.Args) == 0 || len(params) == 0 {
		return false
	}
	id, ok := call.Args[len(call.Args)-1].(*ast.Ident)
	return ok && params[id.Name]
}

// constFormatString folds a string-literal expression — a BasicLit or a
// compile-time concatenation of literals ("count: "+"%d\n") — into its value
// (#533: go vet treats literal concatenation as a constant format string).
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

// usesExplicitIndex reports whether the format contains explicit argument
// indexes like %[1]s, which reuse arguments and defeat naive verb counting.
func usesExplicitIndex(format string) bool {
	return strings.Contains(format, "%[")
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
		if format[i] == '%' {
			i++
			continue
		}
		// Parse a single format directive (index, flags, width, precision, verb).
		if verb, _, ok := parseFormatDirective(format, i); ok {
			count++
			i = verb
		} else {
			i++ // unrecognized; skip to avoid infinite loop
		}
	}
	return count
}

// countFormatArgs counts the ARGUMENTS consumed by a format string: one per
// verb plus one per `*` width/precision (fix #533: `fmt.Printf("%*d", w, v)`
// is valid — the star takes the width from the argument list, matching go
// vet's accounting).
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

// parseFormatDirective parses a printf format directive starting at position
// `start` (the character immediately after the opening %). It skips the
// explicit arg index (%[N]), flags, width, and precision, then checks if the
// next character is a valid verb. Returns the position after the verb, the
// number of `*` width/precision stars (each consumes an argument, #533), and
// true if a verb was found; otherwise returns start, 0, and false.
func parseFormatDirective(format string, start int) (int, int, bool) {
	i := start
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
	// Skip over flags, width, and precision; each `*` in that region
	// consumes an argument (#533), so count them over the whole span.
	region := i
	i = skipWhile(format, i, isFlagWidthChar)
	if i < len(format) && format[i] == '.' {
		i++
		i = skipWhile(format, i, isPrecChar)
	}
	// The next non-flag character is the verb.
	if i < len(format) && isVerbChar(format[i]) {
		return i + 1, strings.Count(format[region:i], "*"), true
	}
	return start, 0, false
}

// isFlagWidthChar matches flag characters, width digits, and the `*` width.
func isFlagWidthChar(b byte) bool {
	return isFlagChar(b) || isDigit(b) || b == '*'
}

// isPrecChar matches precision digits and the `*` precision.
func isPrecChar(b byte) bool {
	return isDigit(b) || b == '*'
}

// skipWhile advances index i past all consecutive bytes for which pred returns
// true, bounded by the string length.
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
