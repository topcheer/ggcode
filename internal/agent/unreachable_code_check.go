package agent

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const maxUnreachableWarnings = 3

func checkUnreachableCode(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" || strings.TrimSpace(newContent) == "" {
		return nil
	}

	goAST, fset, err := parseGoSource(filePath, newContent, 0)
	if err != nil || goAST == nil {
		return nil
	}

	var warnings []string
	var gotoTargetLabels map[string]bool

	ast.Inspect(goAST, func(n ast.Node) bool {
		blk, ok := n.(*ast.BlockStmt)
		if !ok || len(blk.List) < 2 {
			return true
		}

		termIdx := findTerminatingStmt(blk.List)
		if termIdx < 0 {
			return true
		}

		deadStmts := blk.List[termIdx+1:]
		if len(deadStmts) == 0 {
			return true
		}

		if gotoTargetLabels == nil {
			gotoTargetLabels = collectGotoLabels(goAST)
		}
		for _, ds := range deadStmts {
			if ls, ok := ds.(*ast.LabeledStmt); ok && gotoTargetLabels[ls.Label.Name] {
				return true
			}
		}

		pos := fset.Position(deadStmts[0].Pos())

		snippet := renderNode(fset, deadStmts[0])
		if snippet != "" && strings.Contains(oldContent, snippet) {
			return true
		}

		desc := stmtDescription(deadStmts[0])
		warnings = append(warnings, fmt.Sprintf(
			"Unreachable code at %s:%d: statement after %s will never execute. "+
				"Remove the dead code or fix the control flow.",
			filepath.Base(filePath), pos.Line, terminatingStmtName(blk.List[termIdx])))

		if desc != "" {
			warnings = append(warnings, "  Dead code: "+desc)
		}

		return true
	})

	ast.Inspect(goAST, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}

		deadBranch, branchType := analyzeDeadBranch(ifs)
		if deadBranch == nil || len(deadBranch.List) == 0 {
			return true
		}

		pos := fset.Position(deadBranch.Pos())

		snippet := renderNode(fset, deadBranch.List[0])
		if snippet != "" && strings.Contains(oldContent, snippet) {
			return true
		}

		warnings = append(warnings, fmt.Sprintf(
			"Unreachable branch at %s:%d: %s -- this code can never execute. "+
				"Remove the dead branch or fix the condition.",
			filepath.Base(filePath), pos.Line, branchType))

		return true
	})

	if len(warnings) == 0 {
		return nil
	}

	if len(warnings) > maxUnreachableWarnings {
		warnings = warnings[:maxUnreachableWarnings]
	}

	debug.Log("unreachable-code-check", "found %d unreachable code issue(s) in %s", len(warnings), filePath)
	return warnings
}

func renderNode(fset *token.FileSet, node ast.Node) string {
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, node); err != nil {
		return ""
	}
	return buf.String()
}

func collectGotoLabels(file *ast.File) map[string]bool {
	targets := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		if bs, ok := n.(*ast.BranchStmt); ok && bs.Tok == token.GOTO && bs.Label != nil {
			targets[bs.Label.Name] = true
		}
		return true
	})
	return targets
}

func findTerminatingStmt(stmts []ast.Stmt) int {
	for i, s := range stmts {
		if i == len(stmts)-1 {
			break
		}
		switch s.(type) {
		case *ast.ReturnStmt, *ast.BranchStmt:
			return i
		case *ast.ExprStmt:
			if isTerminatingCall(s) {
				return i
			}
		}
	}
	return -1
}

func isTerminatingCall(stmt ast.Stmt) bool {
	exprStmt, ok := stmt.(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := exprStmt.X.(*ast.CallExpr)
	if !ok {
		return false
	}

	var name string
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		name = fn.Name
	case *ast.SelectorExpr:
		ident, ok := fn.X.(*ast.Ident)
		if !ok {
			return false
		}
		name = ident.Name + "." + fn.Sel.Name
	default:
		return false
	}

	switch name {
	case "panic", "log.Fatal", "log.Fatalf", "log.Fatalln",
		"log.Panic", "log.Panicf", "log.Panicln",
		"os.Exit", "runtime.Goexit":
		return true
	}
	return false
}

func terminatingStmtName(stmt ast.Stmt) string {
	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		return "return"
	case *ast.BranchStmt:
		return s.Tok.String()
	case *ast.GoStmt:
		return "go"
	case *ast.ExprStmt:
		if call, ok := s.X.(*ast.CallExpr); ok {
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				return fn.Name + "()"
			case *ast.SelectorExpr:
				if ident, ok := fn.X.(*ast.Ident); ok {
					return ident.Name + "." + fn.Sel.Name + "()"
				}
			}
		}
	}
	return "terminating statement"
}

func stmtDescription(stmt ast.Stmt) string {
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		var lhs []string
		for _, l := range s.Lhs {
			if ident, ok := l.(*ast.Ident); ok {
				lhs = append(lhs, ident.Name)
			}
		}
		return strings.Join(lhs, ", ") + " = ..."
	case *ast.ExprStmt:
		if call, ok := s.X.(*ast.CallExpr); ok {
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				return fn.Name + "(...)"
			case *ast.SelectorExpr:
				if ident, ok := fn.X.(*ast.Ident); ok {
					return ident.Name + "." + fn.Sel.Name + "(...)"
				}
			}
		}
		return "expression"
	case *ast.IfStmt:
		return "if statement"
	case *ast.ForStmt:
		return "for loop"
	case *ast.DeclStmt:
		return "declaration"
	case *ast.IncDecStmt:
		if ident, ok := s.X.(*ast.Ident); ok {
			return ident.Name + s.Tok.String()
		}
		return "increment/decrement"
	}
	return ""
}

func analyzeDeadBranch(ifs *ast.IfStmt) (*ast.BlockStmt, string) {
	lit, ok := ifs.Cond.(*ast.Ident)
	if !ok {
		return nil, ""
	}

	switch lit.Name {
	case "true":
		if ifs.Else != nil {
			if elseBlock, ok := ifs.Else.(*ast.BlockStmt); ok {
				return elseBlock, "else branch of 'if true'"
			}
		}
	case "false":
		return ifs.Body, "'if false' body"
	}
	return nil, ""
}
