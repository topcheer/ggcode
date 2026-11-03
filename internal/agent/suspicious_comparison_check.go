package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
)

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

type suspiciousCmpInstance struct {
	posStr    string
	leftText  string
	rightText string
	op        string
	reason    string
}

func checkSuspiciousComparison(filePath, oldContent, newContent string) string {
	if filepath.Ext(filePath) != ".go" || strings.TrimSpace(newContent) == "" {
		return ""
	}

	if strings.HasSuffix(filePath, "_test.go") {
		return ""
	}

	file, fset, err := parseGoSource(filePath, newContent, parser.AllErrors)
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

func isIdentStart(ch rune) bool {
	return ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

func isIdentPart(ch rune) bool {
	return isIdentStart(ch) || (ch >= '0' && ch <= '9')
}

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

func collectSuspiciousCmps(filePath, oldContent string) map[string]bool {
	if strings.TrimSpace(oldContent) == "" {
		return nil
	}
	oldFile, oldFset, err := parseGoSource(filePath, oldContent, parser.AllErrors)
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

func isNonZeroFloatLiteral(expr ast.Expr) bool {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.FLOAT {
		return false
	}
	v, err := strconv.ParseFloat(lit.Value, 64)
	if err != nil {
		return true
	}
	return v != 0
}

func findConstantConditions(fset *token.FileSet, file *ast.File) []suspiciousCmpInstance {
	var instances []suspiciousCmpInstance

	ast.Inspect(file, func(n ast.Node) bool {
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

			collectConstLiteralCmps(cond, fset, &instances)
		}
		return true
	})

	return instances
}

func collectConstLiteralCmps(cond ast.Expr, fset *token.FileSet, instances *[]suspiciousCmpInstance) {
	switch e := cond.(type) {
	case *ast.BinaryExpr:
		if e.Op == token.LAND || e.Op == token.LOR {
			collectConstLiteralCmps(e.X, fset, instances)
			collectConstLiteralCmps(e.Y, fset, instances)
			return
		}
		if !isConstLiteralCmp(e) {
			return
		}
		pos := fset.Position(e.Pos())
		*instances = append(*instances, suspiciousCmpInstance{
			posStr:    fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line),
			leftText:  exprText(e.X),
			rightText: exprText(e.Y),
			op:        e.Op.String(),
			reason:    "comparison of two constant literals is fixed at compile time (always true/false) - likely a logic error or leftover debug code",
		})
	case *ast.ParenExpr:
		collectConstLiteralCmps(e.X, fset, instances)
	}
}

func isConstLiteralCmp(be *ast.BinaryExpr) bool {
	switch be.Op {
	case token.EQL, token.NEQ, token.LSS, token.GTR, token.LEQ, token.GEQ:
	default:
		return false
	}
	return isNumericLiteral(be.X) && isNumericLiteral(be.Y)
}

func isNumericLiteral(expr ast.Expr) bool {
	lit, ok := expr.(*ast.BasicLit)
	return ok && (lit.Kind == token.INT || lit.Kind == token.FLOAT)
}
