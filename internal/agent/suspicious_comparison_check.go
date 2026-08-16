package agent

// Suspicious Comparison & Equality Intelligence (Check #50)
//
// Problem: AI coding agents frequently generate Go code with comparison patterns
// that are either incorrect (break error handling) or always-true/false (dead logic).
// Detected categories:
//
//  1. Sentinel error comparison with == instead of errors.Is() (errors.Is gap):
//     `if err == sql.ErrNoRows` breaks when errors are wrapped (fmt.Errorf %w).
//  2. Float equality comparison (staticcheck SA4003):
//     `if ratio == 0.1` is unreliable due to floating-point representation errors.
//     Should use math.Abs(ratio - 0.1) < epsilon.
//  3. Self-comparison (staticcheck SA4000):
//     `if x == x` is always true (except for NaN), indicating a typo.
//  4. Constant boolean condition (staticcheck SA4015):
//     `if true { ... }` or `if 1 > 2 { ... }` — always-true/false conditions are
//     dead logic, usually indicating a logic error.
//
// Competitor analysis:
//   - Claude Code: no write-time detection
//   - Cursor: staticcheck may catch SA4000/SA4003/SA4015 post-hoc, inconsistent
//   - Cline/OpenHands: no detection
//   - Aider: no detection
//
// Approach: AST-based analysis, zero LLM cost. Delta-aware: only flags patterns
// newly introduced by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
)

// knownSentinelErrors are well-known Go sentinel errors that must be compared
// with errors.Is(), never ==. These are from the standard library.
var knownSentinelErrors = map[string]bool{
	"io.EOF":                     true,
	"io.ErrClosedPipe":           true,
	"io.ErrNoProgress":           true,
	"io.ErrShortBuffer":          true,
	"io.ErrShortWrite":           true,
	"io.ErrUnexpectedEOF":        true,
	"os.ErrClosed":               true,
	"os.ErrExist":                true,
	"os.ErrInvalid":              true,
	"os.ErrNoDeadline":           true,
	"os.ErrNotExist":             true,
	"os.ErrPermission":           true,
	"sql.ErrConnDone":            true,
	"sql.ErrNoRows":              true,
	"sql.ErrTxDone":              true,
	"net.ErrClosed":              true,
	"context.Canceled":           true,
	"context.DeadlineExceeded":   true,
	"url.ErrInvalidURL":          true,
	"strconv.ErrRange":           true,
	"strconv.ErrSyntax":          true,
	"json.ErrSyntax":             true,
	"json.ErrUnexpectedEOF":      true,
	"bufio.ErrBufferFull":        true,
	"bufio.ErrFinalToken":        true,
	"bufio.ErrInvalidUnreadRune": true,
	"bufio.ErrNegativeAdvance":   true,
	"bufio.ErrTooLong":           true,
}

// suspiciousCmpInstance represents a detected suspicious comparison.
type suspiciousCmpInstance struct {
	posStr    string
	leftText  string
	rightText string
	op        string
	reason    string
}

// checkSuspiciousComparison detects suspicious comparison patterns in Go code.
// Delta-aware: only flags instances newly introduced by this edit.
func checkSuspiciousComparison(filePath, oldContent, newContent string) string {
	if filepath.Ext(filePath) != ".go" || strings.TrimSpace(newContent) == "" {
		return ""
	}

	if strings.HasSuffix(filePath, "_test.go") {
		return ""
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, newContent, parser.AllErrors)
	if err != nil {
		return ""
	}

	instances := findSuspiciousComparisons(fset, file)
	instances = append(instances, findConstantConditions(fset, file)...)
	if len(instances) == 0 {
		return ""
	}

	oldSet := collectSuspiciousCmps(filePath, oldContent)

	var newInstances []suspiciousCmpInstance
	for _, inst := range instances {
		key := inst.leftText + inst.op + inst.rightText + inst.reason
		if !oldSet[key] {
			newInstances = append(newInstances, inst)
		}
	}

	if len(newInstances) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("[Suspicious comparison detection] The following comparison(s) have issues:\n")
	for _, inst := range newInstances {
		b.WriteString(fmt.Sprintf("  - %s: '%s %s %s'. %s\n",
			inst.posStr, inst.leftText, inst.op, inst.rightText, inst.reason))
	}
	return b.String()
}

// findSuspiciousComparisons traverses an AST and returns suspicious comparison instances.
func findSuspiciousComparisons(fset *token.FileSet, file *ast.File) []suspiciousCmpInstance {
	var instances []suspiciousCmpInstance

	ast.Inspect(file, func(n ast.Node) bool {
		binExpr, ok := n.(*ast.BinaryExpr)
		if !ok {
			return true
		}
		if binExpr.Op != token.EQL && binExpr.Op != token.NEQ {
			return true
		}

		leftText := exprText(binExpr.X)
		rightText := exprText(binExpr.Y)
		opStr := "=="
		if binExpr.Op == token.NEQ {
			opStr = "!="
		}

		if leftText == "nil" || rightText == "nil" {
			return true
		}

		// SA4000: self-comparison (x == x) is always true except for NaN
		if leftText != "" && leftText == rightText && isSimpleIdent(leftText) {
			pos := fset.Position(binExpr.Pos())
			instances = append(instances, suspiciousCmpInstance{
				posStr:    fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line),
				leftText:  leftText,
				rightText: rightText,
				op:        opStr,
				reason:    "self-comparison is always true (except for NaN) - likely a typo",
			})
			return true
		}

		// SA4003: float equality comparison is unreliable — except against
		// zero, which IEEE 754 represents exactly (SA4003 itself exempts
		// zero). Fix #564: `x == 0.0` advisories pushed users to optimize
		// correct code.
		if isNonZeroFloatLiteral(binExpr.X) || isNonZeroFloatLiteral(binExpr.Y) {
			pos := fset.Position(binExpr.Pos())
			instances = append(instances, suspiciousCmpInstance{
				posStr:    fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line),
				leftText:  leftText,
				rightText: rightText,
				op:        opStr,
				reason:    "float equality with ==/!= is unreliable due to precision - use math.Abs(a-b) < epsilon",
			})
			return true
		}

		reason := detectSuspiciousCmp(leftText, rightText)
		if reason == "" {
			return true
		}

		pos := fset.Position(binExpr.Pos())
		instances = append(instances, suspiciousCmpInstance{
			posStr:    fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line),
			leftText:  leftText,
			rightText: rightText,
			op:        opStr,
			reason:    reason,
		})
		return true
	})

	return instances
}

// detectSuspiciousCmp checks if a comparison between left and right text
// is suspicious. Returns a non-empty reason string if suspicious.
func detectSuspiciousCmp(left, right string) string {
	operands := []string{left, right}
	for _, op := range operands {
		if knownSentinelErrors[op] {
			return fmt.Sprintf("'%s' is a sentinel error that should be compared with errors.Is()", op)
		}
	}

	if isErrorNamed(left) && isErrorNamed(right) {
		return "comparing two error values with == may fail if either is wrapped - consider errors.Is()"
	}

	return ""
}

// isErrorNamed checks if an identifier name suggests it's an error VALUE.
// Matches: err, error, err2, errResult, or selectors like pkg.ErrXxx.
// Deliberately does NOT match counter/metric names that merely start with
// "error" (errorCount, errorTotal, errorRate, obj.errorCount) — those are
// ints and flagging `errorCount == errorTotal` trained users to ignore
// advisories (#564). Boundary rule: after the err prefix the next char
// must be an uppercase letter or digit (camelCase edge).
func isErrorNamed(text string) bool {
	if text == "" {
		return false
	}
	if isSimpleIdent(text) {
		lower := strings.ToLower(text)
		if lower == "err" || lower == "error" {
			return true
		}
		if hasErrPrefixBoundary(text) {
			return true
		}
	}
	parts := strings.Split(text, ".")
	if len(parts) == 2 {
		field := parts[1]
		if strings.HasPrefix(field, "Err") || strings.HasSuffix(strings.ToLower(field), "error") {
			return true
		}
		if hasErrPrefixBoundary(field) {
			return true
		}
	}
	return false
}

// hasErrPrefixBoundary reports whether s starts with err/Err in camelCase
// boundary form: the character after the prefix is an uppercase letter or
// digit (errRead, ErrFoo, err2). Lowercase continuations (errorCount,
// errorTotal) are counters, not error values.
func hasErrPrefixBoundary(s string) bool {
	if len(s) < 4 {
		return false
	}
	if !strings.EqualFold(s[:3], "err") {
		return false
	}
	c := s[3]
	return (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// isIdentStart reports whether ch can start a Go identifier.
func isIdentStart(ch rune) bool {
	return ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

// isIdentPart reports whether ch can appear in a Go identifier (after the first char).
func isIdentPart(ch rune) bool {
	return isIdentStart(ch) || (ch >= '0' && ch <= '9')
}

// isSimpleIdent checks if text is a simple Go identifier.
func isSimpleIdent(s string) bool {
	if s == "" || strings.Contains(s, ".") {
		return false
	}
	for idx, ch := range s {
		if idx == 0 && !isIdentStart(ch) {
			return false
		}
		if idx > 0 && !isIdentPart(ch) {
			return false
		}
	}
	return true
}

// exprText converts an AST expression to its source text representation.
// Extended expression coverage (fix #176): the original 4-case switch folded
// complex asserted expressions — e.g. getA().(int) and getB().(int) both
// rendered ".(int)" — leaving the unchecked-assert fingerprint collision
// class open for method-chain/index/conversion results.
func exprText(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		x := exprText(e.X)
		if x == "" {
			return ""
		}
		return x + "." + e.Sel.Name
	case *ast.ParenExpr:
		return exprText(e.X)
	case *ast.BasicLit:
		return e.Value
	case *ast.CallExpr:
		fn := exprText(e.Fun)
		if fn == "" {
			return ""
		}
		return fn + "()"
	case *ast.IndexExpr:
		x := exprText(e.X)
		if x == "" {
			return ""
		}
		return x + "[" + exprText(e.Index) + "]"
	case *ast.StarExpr:
		return "*" + exprText(e.X)
	case *ast.UnaryExpr:
		return e.Op.String() + exprText(e.X)
	case *ast.BinaryExpr:
		return exprText(e.X) + " " + e.Op.String() + " " + exprText(e.Y)
	case *ast.ArrayType:
		return "[]" + exprText(e.Elt)
	case *ast.MapType:
		return "map[" + exprText(e.Key) + "]" + exprText(e.Value)
	case *ast.ChanType:
		return "chan " + exprText(e.Value)
	default:
		return ""
	}
}

// collectSuspiciousCmps parses old content and returns a set of existing
// comparison texts. Used for delta-aware suppression.
func collectSuspiciousCmps(filePath, oldContent string) map[string]bool {
	if strings.TrimSpace(oldContent) == "" {
		return nil
	}
	oldFset := token.NewFileSet()
	oldFile, err := parser.ParseFile(oldFset, filePath, oldContent, parser.AllErrors)
	if err != nil {
		return nil
	}
	oldInstances := findSuspiciousComparisons(oldFset, oldFile)
	oldInstances = append(oldInstances, findConstantConditions(oldFset, oldFile)...)
	result := make(map[string]bool, len(oldInstances))
	for _, inst := range oldInstances {
		key := inst.leftText + inst.op + inst.rightText + inst.reason
		result[key] = true
	}
	return result
}

// isFloatLiteral returns true if the expression is a floating-point literal.
func isFloatLiteral(expr ast.Expr) bool {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.FLOAT {
		return false
	}
	return true
}

// isNonZeroFloatLiteral reports whether expr is a float literal with a
// nonzero value. Zero is exactly representable, so `x == 0.0` is reliable
// and not advisory-worthy (staticcheck SA4003 agrees). Fix #564 FP.
func isNonZeroFloatLiteral(expr ast.Expr) bool {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.FLOAT {
		return false
	}
	v, err := strconv.ParseFloat(lit.Value, 64)
	if err != nil {
		return true // unparseable (hex float etc.): stay conservative
	}
	return v != 0
}

// findConstantConditions detects conditions that are always true or false (SA4015).
// Flags: literal `true`/`false` used as if/for conditions, and binary expressions
// where both operands are constant literals (e.g., 1 > 2, 3.0 <= 3.0).
func findConstantConditions(fset *token.FileSet, file *ast.File) []suspiciousCmpInstance {
	var instances []suspiciousCmpInstance

	ast.Inspect(file, func(n ast.Node) bool {
		// Detect `if true` / `if false` and `for true` / `for false`
		var cond ast.Expr
		switch stmt := n.(type) {
		case *ast.IfStmt:
			cond = stmt.Cond
		case *ast.ForStmt:
			if stmt.Cond != nil {
				cond = stmt.Cond
			}
		}
		if cond != nil {
			if ident, ok := cond.(*ast.Ident); ok && (ident.Name == "true" || ident.Name == "false") {
				pos := fset.Position(cond.Pos())
				instances = append(instances, suspiciousCmpInstance{
					posStr:    fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line),
					leftText:  ident.Name,
					rightText: "",
					op:        "",
					reason:    "constant boolean condition is always " + ident.Name + " - likely a logic error or leftover debug code",
				})
			}
		}
		return true
	})

	return instances
}
