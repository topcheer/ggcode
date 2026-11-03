package agent

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

type ccFinding struct {
	msg string
	key string
}

func checkConstantConditional(filePath, oldContent, newContent string) []string {

	if !diff.HasChanges(oldContent, newContent) {
		return nil
	}
	findings := ccScan(filePath, newContent)
	if len(findings) == 0 {
		return nil
	}

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
			oldSeen[f.key]--
			continue
		}
		fresh = append(fresh, f)
	}
	return ccMessages(fresh)
}

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

		return false
	})
	return findings
}

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

func ccBinaryBool(e *ast.BinaryExpr) (bool, bool) {
	switch e.Op {
	case token.LAND, token.LOR:
		return ccLogicalBool(e)
	case token.EQL, token.NEQ, token.LSS, token.GTR, token.LEQ, token.GEQ:
		return ccCompareBool(e)
	}
	return false, false
}

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

func ccCompareBool(e *ast.BinaryExpr) (bool, bool) {

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

	lc, lok := ccConstValue(e.X)
	rc, rok := ccConstValue(e.Y)
	if !lok || !rok {
		return false, false
	}
	return constant.Compare(lc, e.Op, rc), true
}

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
