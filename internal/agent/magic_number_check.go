package agent

// Magic Number Detection for Go source files.
//
// Research basis: LLMs frequently generate bare numeric literals in business
// logic instead of using named constants. This is a well-documented code quality
// issue flagged by staticcheck (ST1013), golangci-lint (gomnd), and SonarQube.
// No AI coding agent currently provides inline post-edit magic number detection.
//
// Detection approach: AST-based scan for numeric literals in specific contexts
// where a named constant would be clearer. Delta-aware by value count: only
// flags numbers whose occurrence count increased in this edit.
//
// What it detects:
//  1. Bare numeric literals in comparisons (x > 100, count < 3)
//  2. Numeric literals in function arguments (make([]T, 10), time.Sleep(500))
//  3. Numeric literals in assignments (timeout = 30, retries = 5)
//
// Exclusions (false positive prevention):
//  - 0, 1, 2 (universally understood)
//  - Const/var declarations (already named)
//  - Test files (magic numbers expected in test data)
//  - Type conversions (int(x), byte(s))

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

const (
	minInterestingNumber = 3
	maxMagicWarnings     = 5
)

func checkMagicNumbers(filePath, oldContent, newContent string) string {
	if filepath.Ext(filePath) != ".go" {
		return ""
	}
	if strings.HasSuffix(filePath, "_test.go") {
		return ""
	}
	if strings.TrimSpace(newContent) == "" {
		return ""
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, newContent, 0)
	if err != nil {
		return ""
	}

	oldValueCounts := countMagicValues(filePath, oldContent)

	collected := collectMagicNumbers(fset, f)

	var found []magicNumberInfo
	// Track how many of each value we've seen so far in new content.
	newValueCounts := make(map[string]int)
	for _, mn := range collected {
		newValueCounts[mn.value]++
		// Delta-aware: skip if old content had >= this many occurrences.
		if newValueCounts[mn.value] <= oldValueCounts[mn.value] {
			continue
		}
		found = append(found, mn)
		if len(found) >= maxMagicWarnings {
			break
		}
	}

	if len(found) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("[Magic number detection in %s] ", filePath))
	b.WriteString("Consider extracting these numeric literals into named constants for clarity and maintainability:\n")
	for _, mn := range found {
		b.WriteString(fmt.Sprintf("  Line %d: %s (in %s)\n", mn.line, mn.value, mn.context))
	}
	b.WriteString("Named constants (const timeoutSeconds = 30) improve readability and simplify future changes.")
	_, _ = b.WriteString("") // ensure builder is used
	return b.String()
}

type magicNumberInfo struct {
	value   string
	line    int
	context string
}

func collectMagicNumbers(fset *token.FileSet, f *ast.File) []magicNumberInfo {
	var results []magicNumberInfo

	ast.Inspect(f, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			if isTypeConversion(node) {
				return true
			}
			for _, arg := range node.Args {
				if info := checkLiteral(fset, arg, "function argument"); info != nil {
					results = append(results, *info)
				}
			}

		case *ast.BinaryExpr:
			if isComparisonOp(node.Op) {
				if info := checkLiteral(fset, node.X, "comparison"); info != nil {
					results = append(results, *info)
				}
				if info := checkLiteral(fset, node.Y, "comparison"); info != nil {
					results = append(results, *info)
				}
			}

		case *ast.AssignStmt:
			for _, rhs := range node.Rhs {
				if info := checkLiteral(fset, rhs, "assignment"); info != nil {
					results = append(results, *info)
				}
			}

		case *ast.ValueSpec:
			return false
		}
		return true
	})

	return results
}

func checkLiteral(fset *token.FileSet, expr ast.Expr, context string) *magicNumberInfo {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.INT {
		return nil
	}

	val := lit.Value
	n, err := parseIntLit(val)
	if err != nil || n < minInterestingNumber {
		return nil
	}

	line := 0
	if fset != nil {
		line = fset.Position(lit.Pos()).Line
	}

	return &magicNumberInfo{
		value:   val,
		line:    line,
		context: context,
	}
}

func countMagicValues(filename, src string) map[string]int {
	counts := make(map[string]int)
	if strings.TrimSpace(src) == "" {
		return counts
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return counts
	}

	for _, mn := range collectMagicNumbers(fset, f) {
		counts[mn.value]++
	}
	return counts
}

func isComparisonOp(op token.Token) bool {
	switch op {
	case token.EQL, token.LSS, token.GTR, token.NEQ, token.LEQ, token.GEQ:
		return true
	}
	return false
}

func isTypeConversion(call *ast.CallExpr) bool {
	id, ok := call.Fun.(*ast.Ident)
	if !ok {
		return false
	}
	switch id.Name {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64", "string", "bool", "byte", "rune",
		"complex64", "complex128", "uintptr":
		return true
	}
	return false
}

func parseIntLit(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		var n int64
		for _, c := range s[2:] {
			d := hexDigit(c)
			if d < 0 {
				return 0, fmt.Errorf("invalid hex digit: %c", c)
			}
			n = n*16 + d
		}
		return n, nil
	}
	if strings.HasPrefix(s, "0b") || strings.HasPrefix(s, "0B") {
		var n int64
		for _, c := range s[2:] {
			if c != '0' && c != '1' {
				return 0, fmt.Errorf("invalid binary digit: %c", c)
			}
			n = n*2 + int64(c-'0')
		}
		return n, nil
	}
	if strings.HasPrefix(s, "0o") || strings.HasPrefix(s, "0O") {
		s = s[2:]
		var n int64
		for _, c := range s {
			if c == '_' {
				continue
			}
			if c < '0' || c > '7' {
				return 0, fmt.Errorf("invalid octal digit: %c", c)
			}
			n = n*8 + int64(c-'0')
		}
		return n, nil
	} else if len(s) > 1 && s[0] == '0' && s[1] >= '0' && s[1] <= '7' {
		var n int64
		for _, c := range s {
			if c < '0' || c > '7' {
				return 0, fmt.Errorf("invalid octal digit: %c", c)
			}
			n = n*8 + int64(c-'0')
		}
		return n, nil
	}
	var n int64
	for _, c := range s {
		if c == '_' {
			continue
		}
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid decimal digit: %c", c)
		}
		n = n*10 + int64(c-'0')
	}
	return n, nil
}

func hexDigit(c rune) int64 {
	switch {
	case c >= '0' && c <= '9':
		return int64(c - '0')
	case c >= 'a' && c <= 'f':
		return int64(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int64(c-'A') + 10
	}
	return -1
}
