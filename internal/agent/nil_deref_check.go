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
//   - Error checks apply scoped semantics (fix #238): inside `if err == nil`
//     bodies the risk is cleared, inside `if err != nil` bodies it remains;
//     a terminating `if err != nil` body (return/panic) clears the risk for
//     the code that follows the guard
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
	// Skip test files (fix #238): panics in tests are less critical.
	if isTestFile(filePath) {
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

	// Delta: count instances in old content and track their positions.
	oldPositions := make(map[string]bool)
	if strings.TrimSpace(oldContent) != "" {
		oldFset := token.NewFileSet()
		oldFile, oldErr := parser.ParseFile(oldFset, filePath, oldContent, parser.AllErrors)
		if oldErr == nil {
			for _, decl := range oldFile.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				for _, inst := range findNilDerefsInFunc(oldFset, fn.Body) {
					oldPositions[inst.posStr] = true
				}
			}
		}
	}

	// Filter to only NEW instances (not present in old content).
	var newInstances []nilDerefInstance
	for _, inst := range instances {
		if !oldPositions[inst.posStr] {
			newInstances = append(newInstances, inst)
		}
	}

	if len(newInstances) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("[nil-deref-after-error] Pointers dereferenced before error check - may panic:\n")
	for _, inst := range newInstances {
		b.WriteString(fmt.Sprintf("  - %s: '%s' is dereferenced before an 'if err != nil' check. "+
			"When the error is non-nil, functions often return nil for the primary value. "+
			"Add `if err != nil { return err }` before using '%s'.\n",
			inst.posStr, inst.varName, inst.varName))
	}
	return b.String()
}

// nilRiskEntry tracks a nil-risk variable and its associated error variable.
type nilRiskEntry struct {
	pos     int    // assignment position
	errName string // associated error variable name
}

// findNilDerefsInFunc analyzes a function body for nil-deref-after-error patterns.
// It processes statements in source order, tracking which variables are nil-risk
// (from multi-return assignments). Error-check `if` statements are handled with
// scope-transfer semantics (fix #238): inside an `if err == nil` body the risk
// is treated as cleared (the safe idiom `v, err := f(); if err == nil { v.Field }`
// must not warn), while inside an `if err != nil` body the risk remains (a
// dereference there is genuinely dangerous). After the statement the prior
// risk state is restored — except when the err != nil body terminates
// (returns or panics), in which case code past the guard implies err == nil.
func findNilDerefsInFunc(fset *token.FileSet, body *ast.BlockStmt) []nilDerefInstance {
	// nilRisk maps variable name to its risk entry (position + associated error var).
	nilRisk := make(map[string]nilRiskEntry)
	var instances []nilDerefInstance

	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		if n == nil {
			return
		}
		ast.Inspect(n, func(node ast.Node) bool {
			// Error-check if statements get scoped handling (#238).
			if is, ok := node.(*ast.IfStmt); ok {
				walkErrorCheckIf(is, nilRisk, walk)
				return false // handled; do not descend generically
			}

			// Track assignments: v, err := f()
			if assign, ok := node.(*ast.AssignStmt); ok {
				processAssignment(assign, nilRisk)
				return true
			}

			// Detect dereferences of nil-risk variables
			if inst := detectNilDeref(fset, node, nilRisk); inst != nil {
				instances = append(instances, inst...)
			}

			return true
		})
	}
	walk(body)

	return instances
}

// processAssignment marks variables as nil-risk when they come from multi-return
// assignments where the last value is likely an error.
func processAssignment(assign *ast.AssignStmt, nilRisk map[string]nilRiskEntry) {
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

	// The non-error LHS values are nil-risk, associated with this error variable.
	for i := 0; i < len(assign.Lhs)-1; i++ {
		ident, ok := assign.Lhs[i].(*ast.Ident)
		if !ok || ident.Name == "_" {
			continue
		}
		// Mark this variable as nil-risk, linked to the specific error variable.
		nilRisk[ident.Name] = nilRiskEntry{
			pos:     int(assign.Pos()),
			errName: errName.Name,
		}
	}
}

// walkErrorCheckIf handles an if statement whose condition compares an error
// variable against nil, applying the scope-transfer semantics of fix #238.
// Non-error-check if statements are walked with unchanged risk state.
func walkErrorCheckIf(is *ast.IfStmt, nilRisk map[string]nilRiskEntry, walk func(ast.Node)) {
	bin, ok := is.Cond.(*ast.BinaryExpr)
	if !ok || !isErrorNilCheck(bin) {
		walk(is.Body)
		walk(is.Else)
		return
	}

	// Extract the error variable name being checked (either operand side).
	errName := ""
	if ident, ok := bin.X.(*ast.Ident); ok && isErrIdent(ident) {
		errName = ident.Name
	} else if ident, ok := bin.Y.(*ast.Ident); ok && isErrIdent(ident) {
		errName = ident.Name
	}

	// Snapshot nil-risk entries linked to this specific error variable.
	saved := make(map[string]nilRiskEntry)
	for k, e := range nilRisk {
		if e.errName == errName {
			saved[k] = e
		}
	}
	clearSaved := func() {
		for k := range saved {
			delete(nilRisk, k)
		}
	}
	restoreSaved := func() {
		for k, v := range saved {
			nilRisk[k] = v
		}
	}

	switch bin.Op {
	case token.EQL: // if err == nil { ... } — value is safe inside the body
		clearSaved()
		walk(is.Body)
		restoreSaved()
		if is.Else != nil { // else implies err != nil: risk applies
			walk(is.Else)
		}
	case token.NEQ: // if err != nil { ... } — value is still at risk inside
		walk(is.Body)
		thenTerminates := ifBodyTerminates(is.Body)
		if thenTerminates {
			clearSaved() // guard exits: code past the if implies err == nil
		}
		if is.Else != nil { // else implies err == nil: safe
			clearSaved()
			walk(is.Else)
			if !thenTerminates {
				// Restore the risk after the else only when the then branch can
				// fall through (#281). When the then branch terminates, code after
				// the if is only reachable via the else (err == nil), so the risk
				// stays permanently cleared.
				restoreSaved()
			}
		}
	}
}

// noReturnSelectorCalls lists `pkg.Ident` call forms that never return
// (log.Fatal* calls os.Exit(1); os.Exit and runtime.Goexit terminate).
// t.Fatal/t.Fatalf also never return, but only inside test files -- for
// simplicity we include them conservatively: recognizing a *few extra*
// terminating bodies only suppresses warnings in test-style guard code,
// which is the intended low-noise direction (#273).
var noReturnSelectorCalls = map[string]bool{
	"log.Fatal":      true,
	"log.Fatalf":     true,
	"log.Fatalln":    true,
	"os.Exit":        true,
	"runtime.Goexit": true,
	"t.Fatal":        true,
	"t.Fatalf":       true,
	"t.Fatalln":      true,
}

// ifBodyTerminates reports whether an if-body always exits (return, panic,
// or a known noreturn call such as log.Fatal / os.Exit), meaning control flow
// past the if statement implies the condition was false.
func ifBodyTerminates(body *ast.BlockStmt) bool {
	if body == nil || len(body.List) == 0 {
		return false
	}
	last := body.List[len(body.List)-1]
	switch st := last.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.ExprStmt:
		if call, ok := st.X.(*ast.CallExpr); ok {
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "panic" {
				return true
			}
			// Noreturn selector calls: log.Fatal, os.Exit, runtime.Goexit, ... (#273)
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok {
					if noReturnSelectorCalls[pkg.Name+"."+sel.Sel.Name] {
						return true
					}
				}
			}
		}
	}
	return false
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
func detectNilDeref(fset *token.FileSet, n ast.Node, nilRisk map[string]nilRiskEntry) []nilDerefInstance {
	var instances []nilDerefInstance

	switch node := n.(type) {
	case *ast.SelectorExpr:
		// x.Field or x.Method()
		if x, ok := node.X.(*ast.Ident); ok {
			if entry, risk := nilRisk[x.Name]; risk {
				pos := fset.Position(node.Pos())
				instances = append(instances, nilDerefInstance{
					posStr:  fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line),
					varName: x.Name,
				})
				_ = entry
				delete(nilRisk, x.Name) // report once
			}
		}

	case *ast.IndexExpr:
		// x[idx] on a pointer to array/slice/map
		if x, ok := node.X.(*ast.Ident); ok {
			if entry, risk := nilRisk[x.Name]; risk {
				pos := fset.Position(node.Pos())
				instances = append(instances, nilDerefInstance{
					posStr:  fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line),
					varName: x.Name,
				})
				_ = entry
				delete(nilRisk, x.Name)
			}
		}

	case *ast.StarExpr:
		// *x
		if x, ok := node.X.(*ast.Ident); ok {
			if entry, risk := nilRisk[x.Name]; risk {
				pos := fset.Position(node.Pos())
				instances = append(instances, nilDerefInstance{
					posStr:  fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line),
					varName: x.Name,
				})
				_ = entry
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
