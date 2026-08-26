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
	cleared bool   // risk suppressed inside an err==nil / v!=nil scope (not a permanent clear)
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
	// #1070: Track block depth to implement block-scoped nilRisk clearing.
	// The top-level function body has depth 0; nested blocks (including
	// brother blocks like separate { } blocks) have depth > 0.
	blockDepth := 0

	walk = func(n ast.Node) {
		if n == nil {
			return
		}
		ast.Inspect(n, func(node ast.Node) bool {
			// #1070: Track block depth and clear nilRisk when entering
			// nested blocks to prevent leakage between brother blocks.
			// The top-level function body (depth 0) retains risk state;
			// nested blocks (depth >= 1) start with a clean state.
			if _, ok := node.(*ast.BlockStmt); ok {
				if blockDepth > 0 {
					// Entering a nested block: clear nilRisk to prevent
					// leakage from brother blocks with same variable names.
					for k := range nilRisk {
						delete(nilRisk, k)
					}
				}
				blockDepth++
				defer func() { blockDepth-- }()
				return true
			}

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
// assignments where the last value is likely an error. Single-value
// reassignments of an already-risky variable are handled too (#533): a
// clearly non-nil RHS (fallback `v = &S{...}`) permanently clears the risk.
func processAssignment(assign *ast.AssignStmt, nilRisk map[string]nilRiskEntry) {
	// Only consider multi-value assignments with at least 2 LHS.
	if len(assign.Lhs) < 2 {
		clearReassignedRisk(assign, nilRisk)
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

// clearReassignedRisk permanently clears the nil risk of a variable that is
// reassigned alone (`v = ...`, #533) when the RHS is provably non-nil: an
// address-of expression (`&S{...}`) or a `new(T)` call. Assignment of `nil`,
// reads of the variable itself (`v = v.Next`), and ordinary calls keep the
// risk — they can still produce nil.
func clearReassignedRisk(assign *ast.AssignStmt, nilRisk map[string]nilRiskEntry) {
	if len(assign.Lhs) != 1 {
		return
	}
	ident, ok := assign.Lhs[0].(*ast.Ident)
	if !ok || ident.Name == "_" {
		return
	}
	if e, risky := nilRisk[ident.Name]; !risky || e.cleared {
		// Not risky, or risk currently suppressed inside an err==nil / v!=nil
		// scope: leave the snapshot semantics (#238) in charge of that scope.
		return
	}
	for _, rhs := range assign.Rhs {
		if isNonNullAssignExpr(rhs) {
			delete(nilRisk, ident.Name)
		}
	}
}

// isNonNullAssignExpr reports whether the expression is provably non-nil as
// the RHS of a pointer assignment.
func isNonNullAssignExpr(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.UnaryExpr: // &S{...} / &T{} — composite address, never nil
		return v.Op == token.AND
	case *ast.CallExpr: // new(T)
		if id, ok := v.Fun.(*ast.Ident); ok {
			return id.Name == "new"
		}
	}
	return false
}

// walkErrorCheckIf handles an if statement whose condition compares an error
// variable against nil, applying the scope-transfer semantics of fix #238.
// #1067: Walks Init statement (e.g., if v, err := f(); cond) to detect
// dereferences before the condition is evaluated.
// #1068: Walks Cond expression to detect dereferences inside conditions
// that are not nil checks (e.g., if v.Field > 0 && err == nil).
// It also recognizes explicit value-nil guards (#533): a terminating
// `if v == nil` body proves v non-nil afterwards, and `v != nil` bodies are
// safe. Non-nil-check if statements are walked with unchanged risk state.
func walkErrorCheckIf(is *ast.IfStmt, nilRisk map[string]nilRiskEntry, walk func(ast.Node)) {
	// #1067: Walk Init statement first (e.g., if v, err := f(); cond)
	if is.Init != nil {
		walk(is.Init)
	}

	bin, ok := is.Cond.(*ast.BinaryExpr)
	if !ok || (bin.Op != token.EQL && bin.Op != token.NEQ) {
		// #1068: Walk Cond for non-nil-check conditions to catch
		// dereferences like "if v.Field > 0 && err == nil"
		walk(is.Cond)
		walk(is.Body)
		walk(is.Else)
		return
	}

	// #533 (C1): explicit value-nil guard on a nil-risk variable.
	if v := valueNilCheckedVar(bin, nilRisk); v != "" {
		walkValueNilCheckIf(is, bin.Op, v, nilRisk, walk)
		return
	}

	if !isErrorNilCheck(bin) {
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
	applyErrNilScopeSemantics(is, bin.Op, errName, nilRisk, walk)
}

// applyErrNilScopeSemantics implements the #238/#281/#533 scope-transfer
// semantics for `if err == nil` / `if err != nil` guards on the given error
// variable: risk is suppressed (not deleted) inside safe branches so that a
// permanent clear made while walking a branch (fallback reassignment, #533)
// or a report-once deletion is never resurrected by the restore.
func applyErrNilScopeSemantics(is *ast.IfStmt, op token.Token, errName string, nilRisk map[string]nilRiskEntry, walk func(ast.Node)) {
	// Snapshot nil-risk entries linked to this specific error variable.
	saved := make(map[string]nilRiskEntry)
	for k, e := range nilRisk {
		if e.errName == errName {
			saved[k] = e
		}
	}
	suppressSaved := func() { setSavedCleared(nilRisk, saved, true) }
	unsuppressSaved := func() { setSavedCleared(nilRisk, saved, false) }
	permanentlyClear := func() {
		for k := range saved {
			delete(nilRisk, k)
		}
	}

	switch op {
	case token.EQL: // if err == nil { ... } — value is safe inside the body
		suppressSaved()
		walk(is.Body)
		unsuppressSaved()
		if is.Else != nil { // else implies err != nil: risk applies
			walk(is.Else)
		}
	case token.NEQ: // if err != nil { ... } — value is still at risk inside
		walk(is.Body)
		thenTerminates := ifBodyTerminates(is.Body)
		if thenTerminates {
			permanentlyClear() // guard exits: code past the if implies err == nil
		}
		if is.Else != nil { // else implies err == nil: safe
			suppressSaved()
			walk(is.Else)
			if !thenTerminates {
				// Restore the risk after the else only when the then branch can
				// fall through (#281). When the then branch terminates, code after
				// the if is only reachable via the else (err == nil), so the risk
				// stays permanently cleared.
				unsuppressSaved()
			}
		}
	}
}

// setVarCleared toggles the suppression flag on one nil-risk entry.
func setVarCleared(nilRisk map[string]nilRiskEntry, name string, cleared bool) {
	if e, ok := nilRisk[name]; ok {
		e.cleared = cleared
		nilRisk[name] = e
	}
}

// setSavedCleared toggles the suppression flag on every snapshotted entry.
func setSavedCleared(nilRisk, saved map[string]nilRiskEntry, cleared bool) {
	for k := range saved {
		setVarCleared(nilRisk, k, cleared)
	}
}

// valueNilCheckedVar returns the variable name when the binary expression is
// a nil comparison (`v == nil` / `nil != v`) against a nil-risk variable that
// is not error-named (#533). Returns "" otherwise.
func valueNilCheckedVar(bin *ast.BinaryExpr, nilRisk map[string]nilRiskEntry) string {
	if bin.Op != token.EQL && bin.Op != token.NEQ {
		return ""
	}
	if x, ok := bin.X.(*ast.Ident); ok && isNilIdent(bin.Y) && !isErrIdent(x) {
		if _, risky := nilRisk[x.Name]; risky {
			return x.Name
		}
	}
	if y, ok := bin.Y.(*ast.Ident); ok && isNilIdent(bin.X) && !isErrIdent(y) {
		if _, risky := nilRisk[y.Name]; risky {
			return y.Name
		}
	}
	return ""
}

// walkValueNilCheckIf applies #533 (C1) semantics for a guard that compares a
// nil-risk value variable against nil:
//
//   - `if v != nil { ... }`: v is non-nil inside the body (risk suppressed);
//     the else branch implies v == nil, so the risk stays active there.
//   - `if v == nil { ... }`: v IS nil inside the body (deref there is a real
//     bug, risk stays active); if the body terminates, code past the guard is
//     only reachable with v != nil, so the risk is permanently cleared. The
//     else branch implies v != nil (suppressed inside).
func walkValueNilCheckIf(is *ast.IfStmt, op token.Token, varName string, nilRisk map[string]nilRiskEntry, walk func(ast.Node)) {
	suppressVar := func() {
		if e, ok := nilRisk[varName]; ok {
			e.cleared = true
			nilRisk[varName] = e
		}
	}
	unsuppressVar := func() {
		if e, ok := nilRisk[varName]; ok {
			e.cleared = false
			nilRisk[varName] = e
		}
	}

	if op == token.NEQ { // if v != nil
		suppressVar()
		walk(is.Body)
		unsuppressVar()
		if is.Else != nil {
			walk(is.Else) // v == nil here; risk applies
		}
		return
	}
	// if v == nil
	walk(is.Body) // v is nil here; deref inside is genuinely dangerous
	if ifBodyTerminates(is.Body) {
		delete(nilRisk, varName) // past a terminating guard v cannot be nil
	}
	if is.Else != nil { // v != nil in the else branch
		suppressVar()
		walk(is.Else)
		unsuppressVar()
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
			if entry, risk := nilRisk[x.Name]; risk && !entry.cleared {
				pos := fset.Position(node.Pos())
				instances = append(instances, nilDerefInstance{
					// #1069: Include variable name in delta key to distinguish
					// different variables on the same line and prevent
					// re-reporting when comments move.
					posStr:  fmt.Sprintf("%s:%d:%s", filepath.Base(pos.Filename), pos.Line, x.Name),
					varName: x.Name,
				})
				_ = entry
				delete(nilRisk, x.Name) // report once
			}
		}

	case *ast.IndexExpr:
		// x[idx] on a pointer to array/slice/map
		if x, ok := node.X.(*ast.Ident); ok {
			if entry, risk := nilRisk[x.Name]; risk && !entry.cleared {
				pos := fset.Position(node.Pos())
				instances = append(instances, nilDerefInstance{
					// #1069: Include variable name in delta key to distinguish
					// different variables on the same line and prevent
					// re-reporting when comments move.
					posStr:  fmt.Sprintf("%s:%d:%s", filepath.Base(pos.Filename), pos.Line, x.Name),
					varName: x.Name,
				})
				_ = entry
				delete(nilRisk, x.Name)
			}
		}

	case *ast.StarExpr:
		// *x
		if x, ok := node.X.(*ast.Ident); ok {
			if entry, risk := nilRisk[x.Name]; risk && !entry.cleared {
				pos := fset.Position(node.Pos())
				instances = append(instances, nilDerefInstance{
					// #1069: Include variable name in delta key to distinguish
					// different variables on the same line and prevent
					// re-reporting when comments move.
					posStr:  fmt.Sprintf("%s:%d:%s", filepath.Base(pos.Filename), pos.Line, x.Name),
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
// Migrated from error_swallow_check.go when that dead detector was deleted
// (#507): this check was its only live consumer.
func looksLikeError(name string) bool {
	switch name {
	case "err", "e", "errs", "retErr", "callErr":
		return true
	default:
		// Match names ending in "Err" or "Error" (e.g., parseErr, dbError).
		if strings.HasSuffix(name, "Err") || strings.HasSuffix(name, "Error") {
			return true
		}
		// Match errN pattern (err1, err2, etc.) - common when handling
		// multiple error-returning calls in the same function.
		if len(name) > 3 && name[:3] == "err" {
			rest := name[3:]
			isDigits := rest != ""
			for _, c := range rest {
				if c < '0' || c > '9' {
					isDigits = false
					break
				}
			}
			if isDigits {
				return true
			}
		}
		return false
	}
}

// isNilIdent returns true if expr is the `nil` identifier.
// Migrated from error_swallow_check.go (#507): shared by isErrorNilCheck.
func isNilIdent(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "nil"
}
