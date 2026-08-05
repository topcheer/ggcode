package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// Dead code detection for Go files after agent edits.
//
// This complements unreachable-code-detection (which catches code after
// terminating statements) with additional dead-code patterns that AI agents
// frequently introduce:
//
//  1. Empty branch bodies - `if cond {}` or `else {}` where the agent removed
//     code but left the branching structure (staticcheck does not flag these).
//  2. Empty function bodies - non-test, non-interface functions with `{}`
//     bodies, indicating incomplete or abandoned implementations.
//  3. Dead assignments - `x := compute(); x = other()` where the first value
//     is never read before being overwritten (ineffassign pattern).
//  4. Unused function parameters - params declared but never referenced in the
//     body, common when AI refactors signatures (varcheck/U1000 pattern).
//
// All checks are deterministic (AST pattern matching, zero LLM cost).

const maxDeadCodeWarnings = 4

// checkDeadCode detects dead-code patterns introduced by the current edit.
// Returns a slice of warning strings (empty if no issues).
func checkDeadCode(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" || strings.TrimSpace(newContent) == "" {
		return nil
	}

	fset := token.NewFileSet()
	goAST, err := parser.ParseFile(fset, filePath, newContent, parser.ParseComments)
	if err != nil || goAST == nil {
		return nil
	}

	var warnings []string

	warnings = append(warnings, detectEmptyBranches(fset, goAST, oldContent)...)
	if len(warnings) < maxDeadCodeWarnings {
		warnings = append(warnings, detectEmptyFuncBodies(fset, goAST, oldContent)...)
	}
	if len(warnings) < maxDeadCodeWarnings {
		warnings = append(warnings, detectDeadAssignments(fset, goAST, oldContent)...)
	}
	if len(warnings) < maxDeadCodeWarnings {
		warnings = append(warnings, detectUnusedParams(fset, goAST, oldContent)...)
	}

	if len(warnings) > maxDeadCodeWarnings {
		warnings = warnings[:maxDeadCodeWarnings]
	}

	debug.Log("integrity", "dead-code check: %d warning(s) in %s", len(warnings), filepath.Base(filePath))
	return warnings
}

// detectEmptyBranches flags if/for/switch/range blocks with empty bodies.
func detectEmptyBranches(fset *token.FileSet, root *ast.File, oldContent string) []string {
	var warnings []string
	comments := root.Comments

	ast.Inspect(root, func(n ast.Node) bool {
		if len(warnings) >= maxDeadCodeWarnings {
			return false
		}

		var body *ast.BlockStmt
		var construct string

		switch node := n.(type) {
		case *ast.IfStmt:
			if node.Body != nil && len(node.Body.List) == 0 {
				body = node.Body
				construct = "if"
			}
		case *ast.ForStmt:
			if node.Body != nil && len(node.Body.List) == 0 {
				body = node.Body
				construct = "for"
			}
		case *ast.SwitchStmt:
			if node.Body != nil && len(node.Body.List) == 0 {
				body = node.Body
				construct = "switch"
			}
		case *ast.RangeStmt:
			if node.Body != nil && len(node.Body.List) == 0 {
				body = node.Body
				construct = "range"
			}
		}

		if body == nil {
			return true
		}

		// Skip if the body has comments (not truly empty).
		if hasCommentsInRange(comments, body.Lbrace, body.Rbrace) {
			return true
		}

		pos := fset.Position(body.Pos())
		if isInTestFile(pos.Filename) {
			return true
		}

		warnings = append(warnings, fmt.Sprintf(
			"Empty %s body at %s:%d - branch has no statements. "+
				"Remove the dead branch or add the missing implementation.",
			construct, filepath.Base(pos.Filename), pos.Line))
		return true
	})

	return warnings
}

// detectEmptyFuncBodies flags non-test, non-interface functions with empty bodies.
func detectEmptyFuncBodies(fset *token.FileSet, root *ast.File, oldContent string) []string {
	var warnings []string
	comments := root.Comments

	for _, decl := range root.Decls {
		if len(warnings) >= maxDeadCodeWarnings {
			break
		}

		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		// Skip interface methods (no body).
		if fn.Body == nil {
			continue
		}

		// Skip if body has statements.
		if len(fn.Body.List) > 0 {
			continue
		}

		// Skip if body has comments (TODO, placeholder, etc).
		if hasCommentsInRange(comments, fn.Body.Lbrace, fn.Body.Rbrace) {
			continue
		}

		// Skip init() and main() - they can legitimately be empty.
		if fn.Name.Name == "init" || fn.Name.Name == "main" {
			continue
		}

		// Skip functions with no params and no return values - these are
		// common test fixtures (func a() {}). Only flag empty bodies when
		// the signature indicates a real implementation was expected.
		if !funcHasRealSignature(fn) {
			continue
		}

		// Skip test functions.
		name := fn.Name.Name
		if strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Benchmark") ||
			strings.HasPrefix(name, "Example") || strings.HasPrefix(name, "Fuzz") {
			continue
		}

		pos := fset.Position(fn.Body.Pos())
		warnings = append(warnings, fmt.Sprintf(
			"Empty function body for %s at %s:%d - function has no implementation. "+
				"Add the implementation or remove the stub.",
			fn.Name.Name, filepath.Base(pos.Filename), pos.Line))
	}

	return warnings
}

// detectDeadAssignments flags variables assigned a value that is never read
// before being overwritten. This is the ineffassign pattern.
func detectDeadAssignments(fset *token.FileSet, root *ast.File, oldContent string) []string {
	var warnings []string

	ast.Inspect(root, func(n ast.Node) bool {
		if len(warnings) >= maxDeadCodeWarnings {
			return false
		}

		blk, ok := n.(*ast.BlockStmt)
		if !ok || len(blk.List) < 2 {
			return true
		}

		assignments := extractAssignTargets(blk.List)

		for i := 0; i < len(blk.List)-1; i++ {
			vars, ok := assignments[i]
			if !ok || len(vars) == 0 {
				continue
			}

			for v, firstAssign := range vars {
				if isReassignedBeforeRead(blk.List, i+1, v) {
					pos := fset.Position(firstAssign)
					warnings = append(warnings, fmt.Sprintf(
						"Dead assignment at %s:%d: variable %q is assigned but the value is "+
							"overwritten before being read. Remove the dead assignment or use the value.",
						filepath.Base(pos.Filename), pos.Line, v))
					break
				}
			}
		}
		return true
	})

	return warnings
}

// detectUnusedParams flags function parameters that are never referenced
// in the function body. This catches the varcheck/U1000 pattern.
func detectUnusedParams(fset *token.FileSet, root *ast.File, oldContent string) []string {
	var warnings []string

	for _, decl := range root.Decls {
		if len(warnings) >= maxDeadCodeWarnings {
			break
		}

		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		// Skip test functions.
		if strings.HasPrefix(fn.Name.Name, "Test") || strings.HasPrefix(fn.Name.Name, "Benchmark") ||
			strings.HasPrefix(fn.Name.Name, "Example") || strings.HasPrefix(fn.Name.Name, "Fuzz") {
			continue
		}

		if fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
			continue
		}

		referenced := collectBodyIdentifiers(fn.Body)

		for _, field := range fn.Type.Params.List {
			for _, name := range field.Names {
				if name.Name == "_" || name.Name == "" {
					continue
				}
				if !referenced[name.Name] {
					pos := fset.Position(name.Pos())
					warnings = append(warnings, fmt.Sprintf(
						"Unused parameter %q in function %s at %s:%d - parameter is declared "+
							"but never used in the function body. Remove it or use it (or rename to _).",
						name.Name, fn.Name.Name, filepath.Base(pos.Filename), pos.Line))
				}
			}
		}
	}

	return warnings
}

// --- Helpers ---

// funcHasRealSignature returns true if the function has at least one
// parameter or at least one return value. Used to distinguish real
// implementations (which should never be empty) from minimal stubs.
func funcHasRealSignature(fn *ast.FuncDecl) bool {
	if fn.Type.Params != nil {
		for _, field := range fn.Type.Params.List {
			if len(field.Names) > 0 {
				return true
			}
		}
	}
	if fn.Type.Results != nil && len(fn.Type.Results.List) > 0 {
		return true
	}
	return false
}

// hasCommentsInRange returns true if any comment falls between the start and
// end token positions (exclusive). Used to detect bodies that have comments
// but no executable statements.
func hasCommentsInRange(comments []*ast.CommentGroup, start, end token.Pos) bool {
	for _, group := range comments {
		for _, c := range group.List {
			if c.Pos() > start && c.Pos() < end {
				return true
			}
		}
	}
	return false
}

// isInTestFile returns true if the filename ends in _test.go.
func isInTestFile(filename string) bool {
	return strings.HasSuffix(filename, "_test.go")
}

// extractAssignTargets returns a map from statement index to a map of
// variable name to assignment position for direct assignment statements.
func extractAssignTargets(stmts []ast.Stmt) map[int]map[string]token.Pos {
	result := make(map[int]map[string]token.Pos)

	for i, stmt := range stmts {
		asgn, ok := stmt.(*ast.AssignStmt)
		if !ok {
			continue
		}

		for _, lhs := range asgn.Lhs {
			ident, ok := lhs.(*ast.Ident)
			if !ok {
				continue
			}
			if result[i] == nil {
				result[i] = make(map[string]token.Pos)
			}
			result[i][ident.Name] = asgn.Pos()
		}
	}

	return result
}

// isReassignedBeforeRead checks if variable v is reassigned in stmts[startIdx:]
// before any read of v. Returns true if a dead assignment is found.
func isReassignedBeforeRead(stmts []ast.Stmt, startIdx int, v string) bool {
	for i := startIdx; i < len(stmts); i++ {
		reads, writes := analyzeStmtAccess(stmts[i], v)

		// If the var is read before being written, the assignment is not dead.
		if reads > 0 {
			return false
		}

		// If the var is written (reassigned) without a prior read, it's dead.
		if writes > 0 {
			return true
		}
	}
	return false
}

// analyzeStmtAccess counts read and write accesses of variable v in a single
// statement. A write occurs in assignment LHS; a read occurs in RHS or any
// other expression context. Does not double-count LHS identifiers as reads.
func analyzeStmtAccess(stmt ast.Stmt, v string) (reads, writes int) {
	asgn, ok := stmt.(*ast.AssignStmt)
	if !ok {
		// Non-assignment: any reference is a read.
		ast.Inspect(stmt, func(n ast.Node) bool {
			if ident, ok := n.(*ast.Ident); ok && ident.Name == v {
				reads++
			}
			return true
		})
		return reads, writes
	}

	// Assignment: LHS is write, RHS is read.
	for _, lhs := range asgn.Lhs {
		ident, ok := lhs.(*ast.Ident)
		if ok && ident.Name == v {
			writes++
		} else {
			// Compound LHS like index expressions read the var.
			reads += countIdentRefs(lhs, v)
		}
	}
	for _, rhs := range asgn.Rhs {
		reads += countIdentRefs(rhs, v)
	}
	return reads, writes
}

// countIdentRefs counts references to name in an expression node.
func countIdentRefs(expr ast.Expr, name string) int {
	count := 0
	ast.Inspect(expr, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok && ident.Name == name {
			count++
		}
		return true
	})
	return count
}

// collectBodyIdentifiers returns a set of all identifier names referenced
// in the function body.
func collectBodyIdentifiers(body *ast.BlockStmt) map[string]bool {
	refs := make(map[string]bool)
	ast.Inspect(body, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok {
			refs[ident.Name] = true
		}
		return true
	})
	return refs
}
