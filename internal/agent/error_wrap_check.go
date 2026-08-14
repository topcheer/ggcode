package agent

// Inconsistent Error Wrapping Detection in Go Code
//
// Problem: AI coding agents frequently produce Go code with broken error
// wrapping. Go 1.13 introduced %w verb for error wrapping, but agents often:
//
//  1. Use %v instead of %w in fmt.Errorf when wrapping errors - this breaks
//     errors.Is() and errors.As() because the error chain is lost. Callers
//     can no longer unwrap or type-assert the wrapped error.
//
//  2. Use errors.New(err.Error()) - this creates a brand-new error from the
//     string representation, losing the original error type and chain entirely.
//     The resulting error is unequal to the original under errors.Is().
//
//  3. Use string concatenation in fmt.Errorf: fmt.Errorf("failed: " + err.Error())
//     - same problem: the error chain is lost and format verbs in the error
//     message can cause corrupted output.
//
// These are subtle bugs because the code compiles, tests may pass (if tests
// don't check error chains), and the error message looks correct. But any
// downstream code using errors.Is(err, os.ErrNotExist) or errors.As(err, &pathErr)
// will silently fail.
//
// Competitor analysis:
//   - Claude Code: no detection (relies on agent judgment)
//   - Cursor: no detection (lint-on-save may catch via staticcheck S1028, but
//     staticcheck is not always installed and doesn't catch all patterns)
//   - Cline/OpenHands: no detection
//   - Aider: no detection
//   - GitHub Copilot: may suggest %w in completions but doesn't verify edits
//
// go vet does NOT flag %v vs %w in Errorf — both are valid format verbs.
// staticcheck's S1028 rule catches errors.New(fmt.Sprintf(...)) but not the
// subtler patterns below. errcheck doesn't cover wrapping semantics at all.
//
// Approach: AST-based analysis of Go source. For each fmt.Errorf and errors.New
// call, inspect the arguments for error-wrapping anti-patterns. Delta-aware:
// only flags patterns introduced by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// errorWrapInstance represents a detected error wrapping issue.
type errorWrapInstance struct {
	pattern string // human-readable pattern description
	pos     token.Pos
}

// checkErrorWrapping performs AST-based error wrapping detection on Go source.
// Returns warnings for newly-introduced inconsistent wrapping patterns.
//
// Parameters:
//   - filePath: path of the written file (used for language detection)
//   - oldContent: the file content before the write ("" for new files)
//   - newContent: the file content after the write
func checkErrorWrapping(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.HasSuffix(filePath, "_test.go") {
		return nil // test code wrapping is less critical
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	newInstances := findErrorWrapIssues(newContent)
	if len(newInstances) == 0 {
		return nil
	}

	// Delta check: compare against old content positions (fix #142).
	var oldPat map[string]bool
	if strings.TrimSpace(oldContent) != "" {
		for _, iss := range findErrorWrapIssues(oldContent) {
			if oldPat == nil {
				oldPat = make(map[string]bool)
			}
			oldPat[iss.pattern] = true
		}
	}

	var warnings []string
	for _, inst := range newInstances {
		if oldPat != nil && oldPat[inst.pattern] {
			continue
		}
		warnings = append(warnings, inst.pattern)
	}

	if len(warnings) > 2 {
		warnings = warnings[:2]
	}

	return warnings
}

// findErrorWrapIssues parses Go source and returns all error wrapping issues,
// ordered by position.
func findErrorWrapIssues(src string) []errorWrapInstance {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil || file == nil {
		return nil
	}

	var instances []errorWrapInstance

	ast.Inspect(file, func(node ast.Node) bool {
		ce, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		fnName := callExprName(ce)
		pos := ce.Pos()

		switch fnName {
		case "fmt.Errorf":
			// Pattern 1: fmt.Errorf("..." + err.Error()) — string concat loses wrapping.
			if isStringConcatWithErrError(ce) {
				instances = append(instances, errorWrapInstance{
					pattern: fmt.Sprintf(
						"Errorf with string concatenation (`%s` at %s): using `err.Error()` "+
							"inside a format string concatenation loses the error chain. "+
							"Use `fmt.Errorf(\"...: %%w\", err)` instead so errors.Is/As work.",
						fnName, fset.Position(pos)),
					pos: pos,
				})
				return true
			}

			// Pattern 2: fmt.Errorf("...%v...", err) — %v loses wrapping chain.
			if issues := checkErrorfVerb(ce, fset, pos); len(issues) > 0 {
				instances = append(instances, issues...)
			}

		case "errors.New":
			// Pattern 3: errors.New(err.Error()) — loses chain and type info.
			if isErrorNewWithErrError(ce) {
				instances = append(instances, errorWrapInstance{
					pattern: fmt.Sprintf(
						"errors.New(err.Error()) at %s: creating a new error from an error's "+
							"string loses the original error type and chain. Use "+
							"fmt.Errorf(\"%%w\", err) to preserve the chain for errors.Is/As.",
						fset.Position(pos)),
					pos: pos,
				})
			}
		}

		return true
	})

	return instances
}

// callExprName extracts the dotted function name from a CallExpr's Fun.
// e.g., "fmt.Errorf" -> "fmt.Errorf", "errors.New" -> "errors.New".
func callExprName(ce *ast.CallExpr) string {
	sel, ok := ce.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return pkg.Name + "." + sel.Sel.Name
}

// isStringConcatWithErrError checks if the first argument to a call is a
// BinaryExpr (+) that involves a .Error() method call anywhere in the
// concatenation chain.
func isStringConcatWithErrError(ce *ast.CallExpr) bool {
	if len(ce.Args) == 0 {
		return false
	}
	return containsErrErrorMethodCall(ce.Args[0])
}

// containsErrErrorMethodCall recursively checks if an expression contains
// a call to .Error() — typically err.Error() or someVar.Error().
func containsErrErrorMethodCall(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.BinaryExpr:
		return containsErrErrorMethodCall(e.X) || containsErrErrorMethodCall(e.Y)
	case *ast.CallExpr:
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok {
			if sel.Sel != nil && sel.Sel.Name == "Error" {
				return true
			}
		}
		// Also check nested calls.
		for _, arg := range e.Args {
			if containsErrErrorMethodCall(arg) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// isErrorNewWithErrError checks if errors.New is called with err.Error() as
// its single argument.
func isErrorNewWithErrError(ce *ast.CallExpr) bool {
	if len(ce.Args) != 1 {
		return false
	}
	return containsErrErrorMethodCall(ce.Args[0])
}

// checkErrorfVerb inspects a fmt.Errorf call for %v used with error arguments
// where %w should be used instead.
func checkErrorfVerb(ce *ast.CallExpr, fset *token.FileSet, pos token.Pos) []errorWrapInstance {
	if len(ce.Args) < 2 {
		return nil
	}

	// The first argument must be a string literal (constant format string).
	lit, ok := ce.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return nil
	}

	formatStr := lit.Value
	// Unquote the string literal.
	if unquoted, err := unquoteString(formatStr); err == nil {
		formatStr = unquoted
	}

	// Extract format verbs from the format string.
	verbs := extractFormatVerbs(formatStr)
	if len(verbs) == 0 {
		return nil
	}

	// The args after the format string correspond to verbs positionally.
	formatArgs := ce.Args[1:]
	if len(verbs) > len(formatArgs) {
		return nil // mismatched, let printf_format_check handle it
	}

	var issues []errorWrapInstance
	for i, verb := range verbs {
		if i >= len(formatArgs) {
			break
		}
		arg := formatArgs[i]

		// %v with an error-typed argument is a wrapping anti-pattern.
		if verb == "%v" && looksLikeErrorArg(arg) {
			// Only flag if there's no %w in the same format string (if there is,
			// the %v might be for a non-error arg — be conservative).
			if !hasWrapVerb(verbs) {
				issues = append(issues, errorWrapInstance{
					pattern: fmt.Sprintf(
						"fmt.Errorf at %s uses %%v for an error argument where %%w should be "+
							"used. With %%v, errors.Is() and errors.As() cannot unwrap the "+
							"causal error. Change to: fmt.Errorf(\"...: %%w\", err).",
						fset.Position(pos)),
					pos: pos,
				})
				break // one warning per Errorf call
			}
		}
	}

	return issues
}

// looksLikeErrorArg heuristically determines if an AST expression likely
// represents an error value. Checks for common error variable names and
// patterns.
func looksLikeErrorArg(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		name := strings.ToLower(e.Name)
		// Common error variable names.
		if name == "err" || name == "e" {
			return true
		}
		// Variables ending in "err" or "error".
		if strings.HasSuffix(name, "err") || strings.HasSuffix(name, "error") {
			return true
		}
		return false
	case *ast.CallExpr:
		// err.Error() is an error being converted to string — not an error arg.
		return false
	default:
		return false
	}
}

// extractFormatVerbs extracts printf-style verbs (%v, %w, %s, %d, etc.) from
// a format string, in order of appearance.
func extractFormatVerbs(format string) []string {
	var verbs []string
	i := 0
	for i < len(format) {
		if format[i] != '%' {
			i++
			continue
		}
		i++ // skip %
		if i >= len(format) {
			break
		}
		// Skip %% (literal percent).
		if format[i] == '%' {
			i++
			continue
		}
		// Skip flags and width.
		for i < len(format) && (format[i] == '+' || format[i] == '-' || format[i] == '#' ||
			format[i] == ' ' || format[i] == '0' || (format[i] >= '0' && format[i] <= '9') ||
			format[i] == '.') {
			i++
		}
		// Handle indexed verbs: %[1]d -> extract 'd'
		if i < len(format) && format[i] == '[' {
			i++ // skip [
			for i < len(format) && format[i] != ']' {
				i++
			}
			if i < len(format) {
				i++ // skip ]
			}
		}
		if i < len(format) {
			verb := "%" + string(format[i])
			verbs = append(verbs, verb)
			i++
		}
	}
	return verbs
}

// hasWrapVerb returns true if the verb list contains %w.
func hasWrapVerb(verbs []string) bool {
	for _, v := range verbs {
		if v == "%w" {
			return true
		}
	}
	return false
}

// unquoteString removes surrounding quotes from a Go string literal.
func unquoteString(s string) (string, error) {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') ||
			(s[0] == '`' && s[len(s)-1] == '`') {
			inner := s[1 : len(s)-1]
			// For double-quoted strings, unescape.
			if s[0] == '"' {
				return unescapeQuotedString(inner), nil
			}
			return inner, nil // raw string
		}
	}
	return s, fmt.Errorf("not a quoted string")
}

// unescapeQuotedString performs minimal unescaping of Go double-quoted string
// content. Handles the most common escape sequences.
func unescapeQuotedString(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			default:
				b.WriteByte(s[i])
			}
		} else {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
