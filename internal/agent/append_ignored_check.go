package agent

// Ignored Return Value From append() Detection
//
// Problem: In Go, append() returns a new slice that must be assigned back.
// Writing `append(slice, item)` as a standalone statement silently discards
// the result -- the original slice is unchanged. This is a common mistake
// for AI-generated code and junior developers alike.
//
// Example bug:
//   append(items, newItem)           // BUG: result discarded, items unchanged
//   items = append(items, newItem)   // CORRECT
//
// The Go compiler does NOT warn about this. staticcheck (SA4017) does detect
// it, but only at lint time, not at write time. No AI coding agent provides
// inline detection.
//
// Competitor analysis:
//   - Claude Code: no write-time detection
//   - Cursor: no detection (relies on external linters)
//   - Cline/OpenHands: no detection
//   - GitHub Copilot: no detection
//   - Aider: no detection
//   - staticcheck SA4017: detects at lint time, not write time
//
// ggcode's approach: AST-based detection of standalone append() calls that
// are not part of an assignment. Uses Go's standard library parser only.
// Zero LLM cost, <1ms per file.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

const maxAppendIgnoredWarnings = 4

// checkAppendIgnored detects standalone append() calls whose return value
// is discarded (not assigned to any variable).
func checkAppendIgnored(filePath, _, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, newContent, 0)
	if err != nil || file == nil {
		return nil
	}

	var warnings []string

	ast.Inspect(file, func(node ast.Node) bool {
		if len(warnings) >= maxAppendIgnoredWarnings {
			return false
		}

		// Case 1: standalone expression statement: append(s, x)
		if exprStmt, ok := node.(*ast.ExprStmt); ok {
			if call, ok := exprStmt.X.(*ast.CallExpr); ok {
				if aiIsBuiltinAppend(call) {
					warnings = append(warnings, fmt.Sprintf(
						"append() return value is discarded at %s -- the original slice is unchanged. "+
							"Assign the result: `slice = append(slice, item)`.",
						fset.Position(call.Pos())))
					return true
				}
			}
		}

		return true
	})

	if len(warnings) >= maxAppendIgnoredWarnings {
		warnings = append(warnings, "...and possibly more discarded append() calls (capped)")
	}

	return warnings
}

// aiIsBuiltinAppend returns true if the call expression is a direct call to
// the built-in append function (no package qualifier).
func aiIsBuiltinAppend(call *ast.CallExpr) bool {
	ident, ok := call.Fun.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == "append"
}
