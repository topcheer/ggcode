package agent

// Context Value Key Type Misuse Detection in Go Code
//
// Problem: AI coding agents frequently write context.WithValue using string
// or numeric literal keys:
//
//	ctx = context.WithValue(ctx, "userID", 42)
//	ctx = context.WithValue(ctx, "request_id", reqID)
//
// The Go standard library documentation explicitly warns:
//   "Do not use string as a key. Using a string as a key collides with
//    other packages using the same string key."
// (https://pkg.go.dev/context#WithValue)
//
// String keys break type safety and cause silent collisions: two unrelated
// packages using context.WithValue(ctx, "id", ...) will overwrite each
// other's values. The correct pattern uses an unexported custom type:
//
//	type ctxKey int
//	const (
//	    userIDKey ctxKey = iota
//	    requestIDKey
//	)
//	ctx = context.WithValue(ctx, userIDKey, 42)
//
// This is a well-known Go anti-pattern that:
//   - go vet does NOT detect (it only checks cancel propagation)
//   - staticcheck does NOT detect (no rule for key types)
//   - gosec does NOT detect (G104 covers unchecked errors, not key types)
//   - No major AI coding tool detects this at write time
//
// Competitor analysis:
//   - Claude Code: no detection
//   - Cursor: no detection (golangci-lint has no rule for this)
//   - Cline/OpenHands: no detection
//   - Aider: no detection
//   - GitHub Copilot: no detection
//
// Approach: AST-based analysis. Detects context.WithValue calls where the
// key argument (second argument) is a basic literal (string, int, float).
// Delta-aware: only flags instances newly introduced by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

const maxCtxKeyWarnings = 4

// ctxKeyInstance represents a detected context key misuse.
type ctxKeyInstance struct {
	line    int
	keyKind string // "string", "int", "float"
}

// checkContextKeyMisuse detects context.WithValue calls using basic literal
// keys (strings, ints, floats) instead of unexported custom type keys.
// Only flags NEW occurrences introduced by this edit (delta-aware).
func checkContextKeyMisuse(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" || strings.TrimSpace(newContent) == "" {
		return nil
	}
	if isTestFile(filePath) {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, newContent, 0)
	if err != nil {
		return nil
	}

	newIssues := findCtxKeyMisuse(fset, file)
	if len(newIssues) == 0 {
		return nil
	}

	// Delta check: compare against old content line numbers.
	var oldLines map[int]bool
	if strings.TrimSpace(oldContent) != "" {
		oldFset := token.NewFileSet()
		oldFile, oldErr := parser.ParseFile(oldFset, filePath, oldContent, 0)
		if oldErr == nil {
			oldIssues := findCtxKeyMisuse(oldFset, oldFile)
			if len(oldIssues) > 0 {
				oldLines = make(map[int]bool, len(oldIssues))
				for _, iss := range oldIssues {
					oldLines[iss.line] = true
				}
			}
		}
	}

	var warnings []string
	for _, iss := range newIssues {
		if oldLines != nil && oldLines[iss.line] {
			continue
		}
		warnings = append(warnings, fmt.Sprintf(
			"L%d: context.WithValue called with a %s literal key. "+
				"The Go standard library explicitly warns against using built-in types "+
				"(especially strings) as context keys because they collide across packages. "+
				"Use an unexported custom type: `type ctxKey int` and pass a constant of that type.",
			iss.line, iss.keyKind))
		// Note: iss.keyKind is always one of "string"/"int"/"float" (no % chars).
		if len(warnings) >= maxCtxKeyWarnings {
			warnings = append(warnings,
				"...and potentially more occurrences (showing first 4)")
			break
		}
	}
	return warnings
}

// findCtxKeyMisuse walks the AST and finds context.WithValue calls where the
// key argument is a basic literal (string, int, float).
func findCtxKeyMisuse(fset *token.FileSet, file *ast.File) []ctxKeyInstance {
	var results []ctxKeyInstance

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		// Check for context.WithValue.
		if !isContextCall(sel, "WithValue") {
			return true
		}

		// WithValue requires at least 3 args: (parent, key, val).
		if len(call.Args) < 2 {
			return true
		}

		keyKind := basicLitKind(call.Args[1])
		if keyKind == "" {
			return true
		}

		line := fset.Position(call.Args[1].Pos()).Line
		results = append(results, ctxKeyInstance{line: line, keyKind: keyKind})
		return true
	})

	return results
}

// isContextCall returns true if the selector expression represents a call to
// context.<fnName>.
func isContextCall(sel *ast.SelectorExpr, fnName string) bool {
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == "context" && sel.Sel.Name == fnName
}

// basicLitKind returns the human-readable kind name if the expression is a
// basic literal with a problematic key type, or "" otherwise.
func basicLitKind(expr ast.Expr) string {
	lit, ok := expr.(*ast.BasicLit)
	if !ok {
		return ""
	}
	switch lit.Kind {
	case token.STRING:
		return "string"
	case token.INT:
		return "int"
	case token.FLOAT:
		return "float"
	default:
		return ""
	}
}
