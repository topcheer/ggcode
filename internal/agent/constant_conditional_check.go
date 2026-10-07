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
	"go/token"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/diff"
)

const maxConstantCondWarnings = 5

// ccFinding pairs a warning message with a structural fingerprint of its
// constant condition (rendered condition text + evaluated constant), used
// for #3491 delta accounting across edits.
type ccFinding struct {
	msg string
	key string
}

// checkConstantConditional detects if-statements whose condition is a
// compile-time boolean constant (always true or always false).
//
// #3491 W4 delta gate: this check used to discard OldContent (`_`) and scan
// NewContent in full, so a file with a pre-existing intentional `if false`
// debug block re-reported it on EVERY unrelated edit (up to 5+1 warnings per
// write), violating the write-integrity W4 contract (deltaGateNew,
// write_integrity.go:101-130) that sibling checks honor (merge-conflict-
// markers, config-syntax, tag-balance, magic-numbers). Now: zero-delta writes
// are skipped entirely, and only conditions NEW to this write are reported,
// matched by condition fingerprint with count subtraction (#1102
// magic-numbers pattern: a second instance of a pre-existing cond still
// surfaces, the pre-existing instance does not).
func checkConstantConditional(filePath, oldContent, newContent string) []string {
	// W4: a zero-delta write (old == new) introduces nothing.
	if !diff.HasChanges(oldContent, newContent) {
		return nil
	}
	findings := ccScan(filePath, newContent)
	if len(findings) == 0 {
		return nil
	}
	// New files / empty old content: everything found is new (keeps the
	// zero-old-content behavior of the 22 pre-existing unit tests intact).
	if strings.TrimSpace(oldContent) == "" {
		return ccMessages(findings)
	}
	oldSeen := make(map[string]int)
	for _, f := range ccScan(filePath, oldContent) {
		oldSeen[f.key]++
	}
	var fresh []ccFinding
	for _, f := range findings {
		if oldSeen[f.key] > 0 {
			oldSeen[f.key]-- // count-subtract: pre-existing instance consumed
			continue
		}
		fresh = append(fresh, f)
	}
	return ccMessages(fresh)
}

// ccScan parses content and returns one finding per constant-conditional
// if-statement, in source order.
func ccScan(filePath, content string) []ccFinding {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(content) == "" {
		return nil
	}

	file, fset, err := parseGoSource(filePath, content, 0)
	if err != nil || file == nil {
		return nil
	}

	var findings []ccFinding
	ast.Inspect(file, func(n ast.Node) bool {
		stmt, ok := n.(*ast.IfStmt)
		if !ok || stmt.Cond == nil {
			return true
		}
		val, isConst := ccBoolValue(stmt.Cond)
		if !isConst {
			return true
		}
		findings = append(findings, ccEmitWarning(stmt, val, fset))
		// Do not descend into the body: code inside a constant-conditional
		// branch is dead, and visiting nested ifs there only adds noise.
		return false
	})
	return findings
}

// ccMessages flattens findings to warning strings, applying the cap only to
// what survived delta filtering (pre-existing conditions never consume cap
// slots).
func ccMessages(findings []ccFinding) []string {
	if len(findings) == 0 {
		return nil
	}
	warnings := make([]string, 0, len(findings))
	for _, f := range findings {
		warnings = append(warnings, f.msg)
	}
	if len(warnings) > maxConstantCondWarnings {
		trunc := fmt.Sprintf("... and %d more constant-conditional warning(s)",
			len(warnings)-maxConstantCondWarnings)
		warnings = warnings[:maxConstantCondWarnings]
		warnings = append(warnings, trunc)
	}
	return warnings
}

// ccEmitWarning builds a warning for a constant-conditional if-statement.
// The fingerprint key (rendered condition text + evaluated constant) is
// position-independent, so an edit that merely shifts a pre-existing
// condition's line number does not make it "new" (#1527 case D rationale).
func ccEmitWarning(stmt *ast.IfStmt, val bool, fset *token.FileSet) ccFinding {
	pos := fset.Position(stmt.Pos())
	key := renderNode(fset, stmt.Cond) + fmt.Sprintf("|const=%v", val)
	if val {
		return ccFinding{
			msg: fmt.Sprintf("%s:%d: if-statement has an always-true condition; "+
				"the else-branch (if any) is dead code. "+
				"Replace with the real predicate or remove the condition.",
				pos.Filename, pos.Line),
			key: key,
		}
	}
	return ccFinding{
		msg: fmt.Sprintf("%s:%d: if-statement has an always-false condition; "+
			"the then-branch is dead code and will never execute. "+
			"This is likely a stubbed-out predicate or a logic bug.",
			pos.Filename, pos.Line),
		key: key,
	}
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
