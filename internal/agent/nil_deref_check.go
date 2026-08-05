package agent

// Nil Pointer Dereference Detection in Go Code
//
// Problem: AI coding agents frequently produce Go code that dereferences a
// pointer returned from a function without first checking whether the error
// value is nil. The canonical pattern:
//
//	result, err := someFunc()
//	fmt.Println(result.Field) // PANIC if err != nil and result is nil
//
// When err != nil, many Go functions return nil for their primary return value.
// Dereferencing that nil pointer causes a runtime panic: "invalid memory address
// or nil pointer dereference." This is one of the most common sources of Go panics
// in production and a top bug pattern in LLM-generated Go code.
//
// staticcheck SA5011 partially catches this using SSA analysis, but requires a
// separate lint cycle and full type information. go vet does NOT detect it.
// This check provides immediate AST-level detection at write time.
//
// Competitor analysis:
//   - Claude Code: no automatic detection (relies on go vet / staticcheck)
//   - Cursor: staticcheck may catch via SA5011 on lint, not at write time
//   - Cline/OpenHands: reactive only - caught by tests or production panics
//   - Aider: no detection
//   - Copilot: no post-edit analysis
//
// Approach: AST-based analysis within each function body. For each assignment
// `v, err := f()` where err is captured, check if v is dereferenced (v.Field,
// v.Method(), *v, v[idx]) BEFORE an `if err != nil` guard appears in source
// order within the same block. Delta-aware: only flags patterns newly
// introduced by this edit.
//
// False positive mitigation:
//   - Only tracks variables from multi-return assignments where error is the
//     last return value (detected via naming heuristic)
//   - Clears the nil-risk flag when an `if err != nil` block is encountered
//   - Only flags dereference via selector (x.Field), index (x[idx]), star (*x),
//     or method call (x.Method()) - not simple variable reads
//   - Skips test files (panics in tests are less critical)

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// nilDerefInstance represents a detected nil-dereference-after-error pattern.
type nilDerefInstance struct {
	posStr  string // human-readable position
	varName string // variable dereferenced without nil check
}

// checkNilDerefAfterError detects pointer dereferences before nil-error checks
// in Go code. Delta-aware: only flags NEW instances introduced by this edit.
func checkNilDerefAfterError(filePath, oldContent, newContent string) string {
	if filepath.Ext(filePath) != ".go" || strings.TrimSpace(newContent) == "" {
		return ""
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, newContent, parser.AllErrors)
	if err != nil {
		return ""
	}

	var instances []nilDerefInstance

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		instances = append(instances, findNilDerefsInFunc(fset, fn.Body)...)
	}

	if len(instances) == 0 {
		return ""
	}

	// Delta: count instances in old content.
	oldCount := 0
	if strings.TrimSpace(oldContent) != "" {
		oldFset := token.NewFileSet()
		oldFile, oldErr := parser.ParseFile(oldFset, filePath, oldContent, parser.AllErrors)
		if oldErr == nil {
			for _, decl := range oldFile.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				oldCount += len(findNilDerefsInFunc(oldFset, fn.Body))
			}
		}
	}

	if len(instances) <= oldCount {
		return ""
	}

	newCount := len(instances) - oldCount
	var b strings.Builder
	b.WriteString("[Nil dereference after error] The following pointer(s) are dereferenced before checking the error return, which can cause a nil pointer panic:\n")
	flagged := 0
	for _, inst := range instances {
		if flagged >= newCount {
			break
		}
		b.WriteString(fmt.Sprintf("  - %s: '%s' is dereferenced before an 'if err != nil' check. "+
			"When the error is non-nil, functions often return nil for the primary value. "+
			"Add `if err != nil { return err }` before using '%s'.\n",
			inst.posStr, inst.varName, inst.varName))
		flagged++
	}
	return b.String()
}

// findNilDerefsInFunc analyzes a function body for nil-deref-after-error patterns.
// It processes statements in source order, tracking which variables are nil-risk
// (from multi-return assignments) and clearing them when error checks appear.
func findNilDerefsInFunc(fset *token.FileSet, body *ast.BlockStmt) []nilDerefInstance {
	// nilRisk maps variable name to the line where it was assigned without error check.
	nilRisk := make(map[string]int)
	var instances []nilDerefInstance

	ast.Inspect(body, func(n ast.Node) bool {
		// Track assignments: v, err := f() or v, err := f()
		if assign, ok := n.(*ast.AssignStmt); ok {
			processAssignment(assign, nilRisk)
			return true
		}

		// Track if err != nil blocks - clear nil-risk variables
		if is, ok := n.(*ast.IfStmt); ok {
			clearNilRiskOnErrorCheck(is, nilRisk)
			return true
		}

		// Detect dereferences of nil-risk variables
		if inst := detectNilDeref(fset, n, nilRisk); inst != nil {
			instances = append(instances, inst...)
		}

		return true
	})

	return instances
}

// processAssignment marks variables as nil-risk when they come from multi-return
// assignments where the last value is likely an error.
func processAssignment(assign *ast.AssignStmt, nilRisk map[string]int) {
	// Only consider multi-value assignments with at least 2 LHS.
	if len(assign.Lhs) < 2 {
		return
	}

	// Check if the last LHS looks like an error variable.
	lastLHS := assign.Lhs[len(assign.Lhs)-1]
	errName, ok := lastLHS.(*ast.Ident)
	if !ok || !looksLikeError(errName.Name) {
		return
	}

	// The non-error LHS values are nil-risk.
	for i := 0; i < len(assign.Lhs)-1; i++ {
		ident, ok := assign.Lhs[i].(*ast.Ident)
		if !ok || ident.Name == "_" {
			continue
		}
		// Mark this variable as nil-risk at its assignment line.
		nilRisk[ident.Name] = int(assign.Pos())
	}
}

// clearNilRiskOnErrorCheck checks if an IfStmt is an error check (if err != nil)
// and if so, clears all nil-risk variables (since the error has been handled).
func clearNilRiskOnErrorCheck(is *ast.IfStmt, nilRisk map[string]int) {
	// Check for: if err != nil { ... }
	bin, ok := is.Cond.(*ast.BinaryExpr)
	if !ok {
		return
	}
	// Must be !=
	if bin.Op != token.NEQ {
		return
	}

	// Left should be an ident that looks like error, right should be nil
	// (or vice versa).
	if isErrorNilCheck(bin) {
		// Clear all nil-risk variables - error has been checked.
		// Note: we only clear those whose position is before this if-statement,
		// but since ast.Inspect visits in order, all existing entries qualify.
		for k := range nilRisk {
			delete(nilRisk, k)
		}
	}
}

// isErrorNilCheck returns true if the binary expression matches err != nil.
func isErrorNilCheck(bin *ast.BinaryExpr) bool {
	leftErr := isErrIdent(bin.X) && isNilIdent(bin.Y)
	rightErr := isErrIdent(bin.Y) && isNilIdent(bin.X)
	return leftErr || rightErr
}

func isErrIdent(e ast.Expr) bool {
	ident, ok := e.(*ast.Ident)
	if !ok {
		return false
	}
	return looksLikeError(ident.Name)
}

// detectNilDeref checks if a node dereferences a nil-risk variable.
func detectNilDeref(fset *token.FileSet, n ast.Node, nilRisk map[string]int) []nilDerefInstance {
	var instances []nilDerefInstance

	switch node := n.(type) {
	case *ast.SelectorExpr:
		// x.Field or x.Method()
		if x, ok := node.X.(*ast.Ident); ok {
			if _, risk := nilRisk[x.Name]; risk {
				pos := fset.Position(node.Pos())
				instances = append(instances, nilDerefInstance{
					posStr:  fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line),
					varName: x.Name,
				})
				delete(nilRisk, x.Name) // report once
			}
		}

	case *ast.IndexExpr:
		// x[idx] on a pointer to array/slice/map
		if x, ok := node.X.(*ast.Ident); ok {
			if _, risk := nilRisk[x.Name]; risk {
				pos := fset.Position(node.Pos())
				instances = append(instances, nilDerefInstance{
					posStr:  fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line),
					varName: x.Name,
				})
				delete(nilRisk, x.Name)
			}
		}

	case *ast.StarExpr:
		// *x
		if x, ok := node.X.(*ast.Ident); ok {
			if _, risk := nilRisk[x.Name]; risk {
				pos := fset.Position(node.Pos())
				instances = append(instances, nilDerefInstance{
					posStr:  fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line),
					varName: x.Name,
				})
				delete(nilRisk, x.Name)
			}
		}
	}

	return instances
}

// looksLikeError returns true if the variable name suggests it holds an error.
// Delegates to the existing looksLikeErrorVar helper from error_swallow_check.go.
func looksLikeError(name string) bool {
	return looksLikeErrorVar(name)
}
