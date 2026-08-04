package agent

// Suspicious Comparison Pattern Detection in Go Code (Check #50)
//
// Problem: AI coding agents frequently generate Go code with comparison patterns
// that silently break error handling when errors are wrapped with fmt.Errorf %w.
// The most common category:
//
//  1. Sentinel error comparison with == instead of errors.Is():
//     `if err == sql.ErrNoRows` breaks when errors are wrapped (fmt.Errorf %w),
//     which is the recommended Go 1.13+ pattern. The comparison silently fails
//     to match, causing the error to propagate as if it were unknown.
//  2. Comparing named error variables from standard library / well-known packages
//     (io.EOF, sql.ErrNoRows, os.ErrNotExist, etc.) with == instead of errors.Is().
//
// Research basis: Go team recommends errors.Is() for ALL sentinel error checks
// since Go 1.13. Static analysis tools (staticcheck SA1029) catch some cases
// but only for known sentinel errors. LLMs trained on pre-Go-1.13 code or
// non-Go languages frequently use == for error comparison.
//
// Competitor analysis:
//   - Claude Code: no detection (relies on go vet)
//   - Cursor: staticcheck may catch SA1029 post-hoc, inconsistent
//   - Cline/OpenHands: no detection
//   - Aider: no detection
//
// Approach: AST-based analysis. For each == or != comparison where one operand
// is a known sentinel error from stdlib, flag it. Delta-aware: only flags
// comparisons newly introduced by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
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
	if len(instances) == 0 {
		return ""
	}

	oldSet := collectSuspiciousCmps(filePath, oldContent)

	var newInstances []suspiciousCmpInstance
	for _, inst := range instances {
		key := inst.leftText + inst.op + inst.rightText
		if !oldSet[key] {
			newInstances = append(newInstances, inst)
		}
	}

	if len(newInstances) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("[Suspicious comparison detection] The following comparison(s) use == or != where errors.Is() should be used:\n")
	for _, inst := range newInstances {
		b.WriteString(fmt.Sprintf("  - %s: '%s %s %s'. %s\n",
			inst.posStr, inst.leftText, inst.op, inst.rightText, inst.reason))
	}
	b.WriteString("Use errors.Is(err, sentinel) instead to support wrapped errors (fmt.Errorf with %w).\n")
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

// isErrorNamed checks if an identifier name suggests it's an error value.
// Matches: err, err2, errResult, or selectors like pkg.ErrXxx.
func isErrorNamed(text string) bool {
	if text == "" {
		return false
	}
	if isSimpleIdent(text) {
		lower := strings.ToLower(text)
		if lower == "err" || strings.HasPrefix(lower, "err") {
			return true
		}
	}
	parts := strings.Split(text, ".")
	if len(parts) == 2 {
		field := parts[1]
		if strings.HasPrefix(field, "Err") || strings.HasSuffix(strings.ToLower(field), "error") {
			return true
		}
		lowerField := strings.ToLower(field)
		if strings.HasPrefix(lowerField, "err") {
			return true
		}
	}
	return false
}

// isSimpleIdent checks if text is a simple Go identifier.
func isSimpleIdent(s string) bool {
	if s == "" || strings.Contains(s, ".") {
		return false
	}
	for idx, ch := range s {
		if idx == 0 {
			if !(ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')) {
				return false
			}
		} else {
			if !(ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')) {
				return false
			}
		}
	}
	return true
}

// exprText converts an AST expression to its source text representation.
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
	result := make(map[string]bool, len(oldInstances))
	for _, inst := range oldInstances {
		key := inst.leftText + inst.op + inst.rightText
		result[key] = true
	}
	return result
}
