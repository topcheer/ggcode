package agent

// Error Swallowing Detection in Go Code
//
// Problem: AI coding agents frequently produce Go code with incomplete error
// handling. The two most common failure modes are:
//
//  1. Empty error check: the agent writes `if err != nil { }` but leaves the
//     body empty (a placeholder it forgot to fill in). The error is silently
//     swallowed and execution continues with potentially invalid state.
//
//  2. Bare return on error: the agent writes `if err != nil { return }` in a
//     function that returns an error value. The bare `return` sends nil/zero
//     values back to the caller, hiding the real error. This is especially
//     dangerous because the calling code has no way to know something went wrong.
//
// Competitor analysis:
//   - Claude Code: no automatic detection (relies on external linters)
//   - Cursor: no automatic detection (lint-on-save may catch via errcheck)
//   - Cline/OpenHands: reactive only -- caught by tests or production incidents
//   - Aider: no automatic detection
//   - GitHub Copilot: sometimes warns via lint integration
//
// External tools like errcheck, staticcheck (S1028), and revive can catch some
// of these, but they require a separate lint cycle and are not always installed.
// This check provides immediate, zero-dependency feedback in <1ms per file
// using Go's standard library AST parser.
//
// Approach: AST-based analysis of Go functions. For each `if err != nil` block,
// inspect the body for:
//   - Empty or comment-only body → "empty error handler" warning
//   - Bare `return` (no expression) in a function that returns error type →
//     "bare return swallows error" warning
//
// Only NEW instances introduced by this edit are flagged (delta-aware) to avoid
// noise on pre-existing code.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// errorSwallowInstance represents a detected error-swallowing pattern.
type errorSwallowInstance struct {
	pattern     string // human-readable pattern description
	pos         token.Pos
	fingerprint string // stable content key (funcName:errName:patternType) for delta matching
}

// checkErrorSwallowing performs AST-based error swallowing detection on Go
// source. Returns warnings for newly-introduced empty error handlers and bare
// returns in error-returning functions.
//
// Parameters:
//   - filePath: path of the written file (used for language detection)
//   - oldContent: the file content before the write ("" for new files)
//   - newContent: the file content after the write
func checkErrorSwallowing(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	// Use content fingerprint matching for robust delta detection.
	// A count-based offset (oldCount+i) is incorrect when new code is prepended
	// because old instances shift to higher indices. token.Pos matching also
	// fails because positions change when code is inserted before existing code.
	// Fingerprint keys (funcName:errName:patternType) are stable regardless of
	// insertion position.

	oldFingerprints := make(map[string]bool)
	for _, inst := range findErrorSwallows(oldContent) {
		oldFingerprints[inst.fingerprint] = true
	}

	var warnings []string
	for _, inst := range findErrorSwallows(newContent) {
		if !oldFingerprints[inst.fingerprint] {
			warnings = append(warnings, inst.pattern)
		}
	}

	return warnings
}

// findErrorSwallows parses Go source and returns all error-swallowing instances
// found, ordered by position.
func findErrorSwallows(src string) []errorSwallowInstance {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil || file == nil {
		return nil
	}

	var instances []errorSwallowInstance

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		returnsError := funcReturnsError(fn.Type)

		ast.Inspect(fn.Body, func(node ast.Node) bool {
			ifStmt, ok := node.(*ast.IfStmt)
			if !ok {
				return true
			}

			errName := extractErrorCheckName(ifStmt.Cond)
			if errName == "" {
				return true
			}

			pos := fset.Position(ifStmt.Pos())

			funcName := ""
			if fn.Name != nil {
				funcName = fn.Name.Name
			}

			// Pattern 1: Empty or comment-only body.
			if isEmptyBody(ifStmt.Body) {
				instances = append(instances, errorSwallowInstance{
					pattern: fmt.Sprintf(
						"Empty error handler: `if %s != nil { }` at %s has an empty body. "+
							"The error is silently ignored. Add error handling (return %s, "+
							"log it, or handle it appropriately).",
						errName, pos, errName),
					pos:         ifStmt.Pos(),
					fingerprint: funcName + ":" + errName + ":empty",
				})
				return true
			}

			// Pattern 2: Bare return in a function that returns error.
			if returnsError && hasBareReturn(ifStmt.Body) {
				instances = append(instances, errorSwallowInstance{
					pattern: fmt.Sprintf(
						"Bare return swallows error: `if %s != nil { return }` at %s in a "+
							"function that returns error. Use `return %s` to propagate the error "+
							"to the caller instead of returning nil.",
						errName, pos, errName),
					pos:         ifStmt.Pos(),
					fingerprint: funcName + ":" + errName + ":bare-return",
				})
			}

			return true
		})
	}

	return instances
}

// funcReturnsError checks if a function type declares an error return value.
func funcReturnsError(ftype *ast.FuncType) bool {
	if ftype == nil || ftype.Results == nil {
		return false
	}
	for _, field := range ftype.Results.List {
		// Named return: field.Names may be non-empty
		// Check the type
		if isIdentError(field.Type) {
			return true
		}
	}
	return false
}

// isIdentError returns true if expr is the builtin `error` type identifier.
func isIdentError(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "error"
}

// extractErrorCheckName inspects an if condition and returns the error
// variable name if the condition is an error check (err != nil or nil != err).
// Returns "" if the condition is not an error-nil check.
func extractErrorCheckName(cond ast.Expr) string {
	binOp, ok := cond.(*ast.BinaryExpr)
	if !ok {
		return ""
	}

	if binOp.Op != token.NEQ {
		return ""
	}

	// Case 1: errName != nil
	if ident, ok := binOp.X.(*ast.Ident); ok {
		if isNilIdent(binOp.Y) && looksLikeErrorVar(ident.Name) {
			return ident.Name
		}
	}

	// Case 2: nil != errName
	if ident, ok := binOp.Y.(*ast.Ident); ok {
		if isNilIdent(binOp.X) && looksLikeErrorVar(ident.Name) {
			return ident.Name
		}
	}

	return ""
}

// isNilIdent returns true if expr is the `nil` identifier.
func isNilIdent(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "nil"
}

// looksLikeErrorVar returns true if the variable name follows Go conventions
// for error variables (err, e, errs, err1, parseErr, etc.).
func looksLikeErrorVar(name string) bool {
	switch name {
	case "err", "e", "errs", "retErr", "callErr":
		return true
	default:
		// Match names ending in "Err" or "Error" (e.g., parseErr, dbError).
		if strings.HasSuffix(name, "Err") || strings.HasSuffix(name, "Error") {
			return true
		}
		// Match errN pattern (err1, err2, etc.) - common when handling
		// multiple error-returning calls in the same function.
		if len(name) > 3 && name[:3] == "err" {
			rest := name[3:]
			isDigits := rest != ""
			for _, c := range rest {
				if c < '0' || c > '9' {
					isDigits = false
					break
				}
			}
			if isDigits {
				return true
			}
		}
		return false
	}
}

// isEmptyBody returns true if a block statement has no statements (or only
// contains comments, which are not stored in the AST as statements).
func isEmptyBody(body *ast.BlockStmt) bool {
	return body == nil || len(body.List) == 0
}

// hasBareReturn returns true if the block contains a return statement with no
// return values (a bare `return`). This is significant in error-returning
// functions because it returns zero values, hiding the error.
func hasBareReturn(body *ast.BlockStmt) bool {
	if body == nil {
		return false
	}

	for _, stmt := range body.List {
		switch s := stmt.(type) {
		case *ast.ReturnStmt:
			// Bare return: return with no results.
			if len(s.Results) == 0 {
				return true
			}
		case *ast.BlockStmt:
			// Nested block (e.g., if-else inside error handler).
			if hasBareReturn(s) {
				return true
			}
		case *ast.IfStmt:
			// Nested if inside the error handler.
			if hasBareReturn(s.Body) {
				return true
			}
			if s.Else != nil {
				if elseBlock, ok := s.Else.(*ast.BlockStmt); ok {
					if hasBareReturn(elseBlock) {
						return true
					}
				}
			}
		}
	}

	return false
}
