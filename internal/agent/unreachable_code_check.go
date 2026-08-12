package agent

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// Unreachable / dead code detection for Go files after agent edits.
//
// Research basis: "Dead code elimination" is a classic compiler optimization,
// but AI agents frequently INTRODUCE unreachable code during refactoring:
//   - Adding an early return but leaving code after it
//   - Inserting a panic() or log.Fatal() call but leaving cleanup code below
//   - Writing `if true { ... } else { ... }` with code in the dead branch
//   - Adding a break/continue but leaving code after it in the loop body
//
// go vet's "unreachable code" check covers case #1 (code after terminating
// statements) but only at the statement level within a block. It does NOT
// catch dead branches (if false), and agents don't always run go vet inline.
// This check provides immediate feedback in the same iteration.
//
// Delta-aware: only flags unreachable code introduced by this edit.

// maxUnreachableWarnings caps the number of unreachable-code warnings per write.
const maxUnreachableWarnings = 3

// checkUnreachableCode detects code that can never execute due to preceding
// terminating statements or impossible branches. Only fires for .go files and
// only for issues introduced by the current edit (delta-aware via text matching).
//
// Returns a slice of warning strings (empty if no issues).
func checkUnreachableCode(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" || strings.TrimSpace(newContent) == "" {
		return nil
	}

	fset := token.NewFileSet()
	goAST, err := parser.ParseFile(fset, filePath, newContent, 0)
	if err != nil || goAST == nil {
		return nil
	}

	var warnings []string

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

		pos := fset.Position(deadStmts[0].Pos())

		// Delta filter: render the dead statement to text and check old content.
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

	// Check for dead branches: if false { ... } or if true { ... } else { ... }
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

		// Delta filter via text rendering.
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

// renderNode serializes an AST node back to source text using go/format.
// Returns empty string on error.
func renderNode(fset *token.FileSet, node ast.Node) string {
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, node); err != nil {
		return ""
	}
	return buf.String()
}

// findTerminatingStmt returns the index of the first statement in the list that
// unconditionally terminates control flow (return, panic, continue, break, goto),
// or -1 if none found. Only non-last terminators matter (those make subsequent
// code unreachable).
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

// isTerminatingCall checks if an expression statement is a call to a function
// that never returns (panic, log.Fatal, log.Panic, os.Exit, runtime.Goexit).
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

// terminatingStmtName returns a human-readable name for the terminating statement.
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

// stmtDescription returns a short description of a statement for warning context.
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

// analyzeDeadBranch checks an if statement for dead branches caused by
// constant boolean conditions (if true / if false).
// Returns the dead branch (if any) and a description string.
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
