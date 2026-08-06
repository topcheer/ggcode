package agent

// Constant Conditional Detection (if true / if false)
//
// Problem: AI coding agents sometimes emit conditions that are constant at
// compile time, such as `if true {}`, `if false {}`, `if 1 == 1 {}`, or
// `if !true {}`. These are almost always bugs:
//
//   - `if false { ... }`: the entire then-branch is dead code. It was likely
//     meant to be `if !cond` or a real predicate, and the dead block hides
//     logic that will never execute.
//   - `if true { ... } else { ... }`: the else-branch is dead code. This is
//     often a half-finished refactor where a condition was stubbed out.
//   - `if 1 == 1 {}` / `if 2 > 3 {}`: constant comparisons from templating
//     mistakes or copy-paste errors.
//
// Unlike runtime-only tools, this check catches the issue at write time with
// zero LLM cost via pure AST constant evaluation.
//
// Competitor analysis:
//   - Claude Code / Cursor / OpenHands / Aider: no write-time detection
//   - staticcheck (SA4023): flags impossible comparisons but not literal
//     `if true`/`if false` dead branches uniformly
//   - golangci-lint: relies on staticcheck; no dedicated constant-condition
//     check for write-time feedback
//
// Approach: AST-based. Parse the file, walk all IfStmt nodes, and evaluate
// the condition expression to a compile-time boolean constant using
// go/constant. Supports:
//   - Boolean literals: true, false
//   - Unary negation: !true, !false
//   - Logical operators: &&, || with constant operands
//   - Comparison operators: ==, !=, <, >, <=, >= with constant operands
//     (numeric basic literals with optional unary +/- and parentheses)
//
// Zero LLM cost. No new external dependencies (go/constant is stdlib).

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

const maxConstantCondWarnings = 5

// checkConstantConditional detects if-statements whose condition is a
// compile-time boolean constant (always true or always false).
func checkConstantConditional(filePath, _, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	src := strings.TrimSpace(newContent)
	if src == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, newContent, 0)
	if err != nil || file == nil {
		return nil
	}

	var warnings []string
	ast.Inspect(file, func(n ast.Node) bool {
		stmt, ok := n.(*ast.IfStmt)
		if !ok || stmt.Cond == nil {
			return true
		}
		val, isConst := ccBoolValue(stmt.Cond)
		if !isConst {
			return true
		}
		ccEmitWarning(stmt, val, fset, &warnings)
		// Do not descend into the body: code inside a constant-conditional
		// branch is dead, and visiting nested ifs there only adds noise.
		return false
	})

	if len(warnings) > maxConstantCondWarnings {
		trunc := fmt.Sprintf("... and %d more constant-conditional warning(s)",
			len(warnings)-maxConstantCondWarnings)
		warnings = warnings[:maxConstantCondWarnings]
		warnings = append(warnings, trunc)
	}
	return warnings
}

// ccEmitWarning appends a warning for a constant-conditional if-statement.
func ccEmitWarning(stmt *ast.IfStmt, val bool, fset *token.FileSet, warnings *[]string) {
	pos := fset.Position(stmt.Pos())
	if val {
		*warnings = append(*warnings,
			fmt.Sprintf("%s:%d: if-statement has an always-true condition; "+
				"the else-branch (if any) is dead code. "+
				"Replace with the real predicate or remove the condition.",
				pos.Filename, pos.Line))
		return
	}
	*warnings = append(*warnings,
		fmt.Sprintf("%s:%d: if-statement has an always-false condition; "+
			"the then-branch is dead code and will never execute. "+
			"This is likely a stubbed-out predicate or a logic bug.",
			pos.Filename, pos.Line))
}

// ccBoolValue evaluates an expression to a compile-time boolean constant.
// Returns (value, true) if constant; (false, false) otherwise.
func ccBoolValue(expr ast.Expr) (bool, bool) {
	switch e := expr.(type) {
	case *ast.Ident:
		if e.Name == "true" {
			return true, true
		}
		if e.Name == "false" {
			return false, true
		}
		return false, false
	case *ast.UnaryExpr:
		if e.Op != token.NOT {
			return false, false
		}
		v, ok := ccBoolValue(e.X)
		if !ok {
			return false, false
		}
		return !v, true
	case *ast.ParenExpr:
		return ccBoolValue(e.X)
	case *ast.BinaryExpr:
		return ccBinaryBool(e)
	}
	return false, false
}

// ccBinaryBool evaluates a binary expression with constant operands to a
// boolean constant. Handles logical (&&, ||) and comparison operators.
func ccBinaryBool(e *ast.BinaryExpr) (bool, bool) {
	switch e.Op {
	case token.LAND, token.LOR:
		return ccLogicalBool(e)
	case token.EQL, token.NEQ, token.LSS, token.GTR, token.LEQ, token.GEQ:
		return ccCompareBool(e)
	}
	return false, false
}

// ccLogicalBool evaluates && and || with constant boolean operands.
func ccLogicalBool(e *ast.BinaryExpr) (bool, bool) {
	l, lok := ccBoolValue(e.X)
	r, rok := ccBoolValue(e.Y)
	if !lok || !rok {
		return false, false
	}
	if e.Op == token.LAND {
		return l && r, true
	}
	return l || r, true
}

// ccCompareBool evaluates a comparison operator on constant operands.
// Operands may be boolean constants (for ==/!=) or numeric constants.
func ccCompareBool(e *ast.BinaryExpr) (bool, bool) {
	// Boolean comparison for == and != (e.g. true == false).
	if e.Op == token.EQL || e.Op == token.NEQ {
		lb, lok := ccBoolValue(e.X)
		rb, rok := ccBoolValue(e.Y)
		if lok && rok {
			if e.Op == token.EQL {
				return lb == rb, true
			}
			return lb != rb, true
		}
	}
	// Numeric comparison (e.g. 1 > 2, 3 == 3).
	lc, lok := ccConstValue(e.X)
	rc, rok := ccConstValue(e.Y)
	if !lok || !rok {
		return false, false
	}
	return constant.Compare(lc, e.Op, rc), true
}

// ccConstValue evaluates an expression to a compile-time numeric/unknown
// constant.Value. Supports basic literals, parentheses, and unary +/-.
func ccConstValue(expr ast.Expr) (constant.Value, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		v := constant.MakeFromLiteral(e.Value, e.Kind, 0)
		return v, v.Kind() != constant.Unknown
	case *ast.ParenExpr:
		return ccConstValue(e.X)
	case *ast.UnaryExpr:
		if e.Op != token.SUB && e.Op != token.ADD {
			return nil, false
		}
		v, ok := ccConstValue(e.X)
		if !ok {
			return nil, false
		}
		return constant.UnaryOp(e.Op, v, 0), true
	}
	return nil, false
}
