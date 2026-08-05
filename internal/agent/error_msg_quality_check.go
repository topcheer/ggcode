package agent

// Error Message Quality Intelligence in Go Code
//
// Problem: AI coding agents frequently produce Go code with low-quality error
// messages that waste debugging time. While existing checks (error_wrap_check,
// error_swallow_check, ignored_error_check) detect structural error-handling
// anti-patterns (wrong wrapping verb, swallowed errors, discarded returns),
// NONE inspect the TEXT of the error message itself.
//
// The three patterns this check catches:
//
//  1. Empty error message: `errors.New("")` or `fmt.Errorf("")` - produces an
//     error with zero debugging context. Callers see the error but have no idea
//     what went wrong or where. This often happens when an LLM leaves a
//     placeholder it forgot to fill.
//
//  2. Generic/vague error message: `errors.New("error")`, `errors.New("failed")`,
//     `errors.New("something went wrong")` - messages that convey no actionable
//     information. An error message should identify WHAT operation failed and
//     WHY, not just state that "an error occurred".
//
//  3. Context-free wrapping: `fmt.Errorf("%w", err)` - wrapping an error with
//     no additional context. The wrapped error adds nothing that the original
//     didn't already have. Good wrapping adds the current operation context:
//     `fmt.Errorf("parsing config: %w", err)`.
//
// These are quality issues, not bugs - the code compiles and works, but
// debugging production incidents becomes significantly harder when error
// messages lack context.
//
// Competitor analysis:
//   - Semgrep: has rules for empty error messages but requires external lint cycle
//   - SonarQube: detects some generic messages but not at write time
//   - CodeRabbit: may comment on error quality in PR review (reactive)
//   - Claude Code / Cursor / Copilot: no inline detection
//   - go vet: does not inspect error message quality
//   - staticcheck: does not flag generic error messages
//
// Approach: AST-based analysis of Go source. For each errors.New and
// fmt.Errorf call, inspect the string literal (first argument) for quality.
// Delta-aware: only flags patterns newly introduced by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// genericErrorMessages is a curated set of low-information error messages that
// LLMs frequently produce. These convey no actionable debugging context.
// Matched case-insensitively after trimming whitespace and trailing punctuation.
var genericErrorMessages = map[string]bool{
	// Bare nouns with no context.
	"error": true, "err": true, "failed": true, "failure": true, "fail": true,
	"bad": true, "oops": true, "oopsie": true,

	// Vague phrases.
	"something went wrong": true,
	"an error occurred":    true,
	"error occurred":       true,
	"unexpected error":     true,
	"internal error":       true,
	"unknown error":        true,
	"something failed":     true,
	"operation failed":     true,
	"operation error":      true,
	"unexpected failure":   true,
	"generic error":        true,
	"unexpected":           true,
	"unknown":              true,
	"undefined":            true,
	"nil pointer":          true,
	"not implemented":      true,
	"todo":                 true,
	"fixme":                true,
	"placeholder":          true,
	"placeholder error":    true,
	"unhandled":            true,
}

// errMsgQualityInstance represents a detected error message quality issue.
type errMsgQualityInstance struct {
	pattern string
	pos     token.Pos
}

// checkErrorMsgQuality performs AST-based error message quality detection on
// Go source. Returns warnings for newly-introduced low-quality error messages.
//
// Parameters:
//   - filePath: path of the written file (used for language detection)
//   - oldContent: the file content before the write ("" for new files)
//   - newContent: the file content after the write
func checkErrorMsgQuality(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.HasSuffix(filePath, "_test.go") {
		return nil // test code uses sentinel errors frequently
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	oldCount := len(findErrMsgQualityIssues(oldContent))
	newInstances := findErrMsgQualityIssues(newContent)
	if len(newInstances) <= oldCount {
		return nil
	}

	newCount := len(newInstances) - oldCount
	var warnings []string
	for i := 0; i < newCount && i+oldCount < len(newInstances); i++ {
		warnings = append(warnings, newInstances[oldCount+i].pattern)
	}

	if len(warnings) > 3 {
		warnings = warnings[:3]
	}

	return warnings
}

// findErrMsgQualityIssues parses Go source and returns all error message
// quality issues, ordered by position.
func findErrMsgQualityIssues(src string) []errMsgQualityInstance {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil || file == nil {
		return nil
	}

	var instances []errMsgQualityInstance

	ast.Inspect(file, func(node ast.Node) bool {
		ce, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		fnName := callExprName(ce)
		pos := ce.Pos()

		switch fnName {
		case "fmt.Errorf":
			if issue := checkErrorfQuality(ce, fset, pos); issue != nil {
				instances = append(instances, *issue)
			}
		case "errors.New":
			if issue := checkErrorNewQuality(ce, fset, pos); issue != nil {
				instances = append(instances, *issue)
			}
		}

		return true
	})

	return instances
}

// checkErrorfQuality inspects a fmt.Errorf call for low-quality messages.
// Returns an issue if the format string is empty, generic, or context-free wrapping.
func checkErrorfQuality(ce *ast.CallExpr, fset *token.FileSet, pos token.Pos) *errMsgQualityInstance {
	if len(ce.Args) == 0 {
		return nil
	}

	lit, ok := ce.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return nil // non-literal format string - can't analyze statically
	}

	msg, err := unquoteString(lit.Value)
	if err != nil {
		return nil
	}
	msgLower := strings.ToLower(strings.TrimRight(strings.TrimSpace(msg), ".,!?;:"))

	// Pattern 1: Empty format string.
	if msgLower == "" {
		return &errMsgQualityInstance{
			pattern: fmt.Sprintf(
				"Empty error message in fmt.Errorf at %s: the format string is empty, "+
					"producing an error with zero debugging context. Add a message "+
					"describing what operation failed and why.",
				fset.Position(pos)),
			pos: pos,
		}
	}

	// Pattern 2: Context-free wrapping - format string is ONLY %%w.
	if msgLower == "%w" {
		return &errMsgQualityInstance{
			pattern: fmt.Sprintf(
				"Context-free error wrapping at %s: fmt.Errorf(\"%%w\", err) adds no "+
					"context beyond the original error. Include the current operation: "+
					"fmt.Errorf(\"doing X: %%w\", err) so logs and stack traces show the call chain.",
				fset.Position(pos)),
			pos: pos,
		}
	}

	// Pattern 3: Generic message text.
	if genericErrorMessages[msgLower] {
		return &errMsgQualityInstance{
			pattern: fmt.Sprintf(
				"Generic error message in fmt.Errorf at %s: %q conveys no actionable "+
					"debugging context. Describe what specifically failed and include relevant "+
					"values: fmt.Errorf(\"failed to parse config %%s: %%w\", path, err).",
				fset.Position(pos), msg),
			pos: pos,
		}
	}

	return nil
}

// checkErrorNewQuality inspects an errors.New call for low-quality messages.
// Returns an issue if the message string is empty or generic.
func checkErrorNewQuality(ce *ast.CallExpr, fset *token.FileSet, pos token.Pos) *errMsgQualityInstance {
	if len(ce.Args) == 0 {
		return nil
	}

	lit, ok := ce.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return nil // non-literal - can't analyze statically
	}

	msg, err := unquoteString(lit.Value)
	if err != nil {
		return nil
	}
	msgLower := strings.ToLower(strings.TrimRight(strings.TrimSpace(msg), ".,!?;:"))

	// Pattern 1: Empty message.
	if msgLower == "" {
		return &errMsgQualityInstance{
			pattern: fmt.Sprintf(
				"Empty error message in errors.New at %s: creates an error with zero "+
					"debugging context. Add a message describing what went wrong.",
				fset.Position(pos)),
			pos: pos,
		}
	}

	// Pattern 2: Generic message.
	if genericErrorMessages[msgLower] {
		return &errMsgQualityInstance{
			pattern: fmt.Sprintf(
				"Generic error message in errors.New at %s: %q conveys no actionable "+
					"context. Describe what specifically failed, e.g. "+
					"errors.New(\"config file not found\") or include the relevant identifier.",
				fset.Position(pos), msg),
			pos: pos,
		}
	}

	return nil
}
