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
	posStr    string // human-readable position (display only)
	key       string // #1128: position-independent delta key (fnName|path|var)
	suffixKey string // #1179: rename-tolerant fallback key (path|var, no fnName)
	varName   string // variable dereferenced without nil check
}

// nilDerefCtx carries per-function context used to build delta keys (#1128).
type nilDerefCtx struct {
	fset   *token.FileSet
	fnName string
}

// collectOldNilDerefIndex parses oldContent and returns the key sets of every
// nil-deref instance already present before the edit (#1128, extended by
// #1179). exact holds position-independent keys (fnName|path|var); suffix
// holds name-independent keys (path|var) used as a fallback when a rename or
// extraction changed the function name component. Keys from a file that fails
// to parse are treated as absent, matching the previous inline behavior.
type nilDerefDeltaIndex struct {
	exact  map[string]bool
	suffix map[string]bool
}

func collectOldNilDerefIndex(filePath, oldContent string) nilDerefDeltaIndex {
	idx := nilDerefDeltaIndex{exact: make(map[string]bool), suffix: make(map[string]bool)}
	if strings.TrimSpace(oldContent) == "" {
		return idx
	}
	oldFset := token.NewFileSet()
	oldFile, oldErr := parser.ParseFile(oldFset, filePath, oldContent, parser.AllErrors)
	if oldErr != nil {
		return idx
	}
	for _, decl := range oldFile.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		for _, inst := range findNilDerefsInFunc(oldFset, fn) {
			idx.exact[inst.key] = true
			idx.suffix[inst.suffixKey] = true
		}
	}
	return idx
}

// formatNilDerefReport renders the warning text for newly introduced
// nil-deref instances.
func formatNilDerefReport(instances []nilDerefInstance) string {
	var b strings.Builder
	b.WriteString("[nil-deref-after-error] Pointers dereferenced before error check - may panic:\n")
	for _, inst := range instances {
		b.WriteString(fmt.Sprintf("  - %s: '%s' is dereferenced before an 'if err != nil' check. "+
			"When the error is non-nil, functions often return nil for the primary value. "+
			"Add `if err != nil { return err }` before using '%s'.\n",
			inst.posStr, inst.varName, inst.varName))
	}
	return b.String()
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
		instances = append(instances, findNilDerefsInFunc(fset, fn)...)
	}

	// Delta: drop instances whose key already exists in old content (#1128:
	// keys are stable across line insertions above). #1179: a rename or
	// extraction changes the function-name component of the exact key while
	// the finding itself is unchanged, which re-reported pre-existing
	// instances as new. Fall back to the name-independent suffix key so
	// renamed code stays suppressed; a pattern absent from the old content
	// still reports as genuinely new.
	oldIdx := collectOldNilDerefIndex(filePath, oldContent)
	var newInstances []nilDerefInstance
	for _, inst := range instances {
		if oldIdx.exact[inst.key] || oldIdx.suffix[inst.suffixKey] {
			continue
		}
		newInstances = append(newInstances, inst)
	}

	if len(newInstances) == 0 {
		return ""
	}
	return formatNilDerefReport(newInstances)
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
// risk state is restored - except when the err != nil body terminates
// (returns or panics), in which case code past the guard implies err == nil.
// nilDerefWalker carries the per-function state shared by the recursive
// statement walker introduced for #1127 (block-scoped risk bookkeeping).
type nilDerefWalker struct {
	ctx       *nilDerefCtx
	nilRisk   map[string]nilRiskEntry
	instances []nilDerefInstance
}

func findNilDerefsInFunc(fset *token.FileSet, fn *ast.FuncDecl) []nilDerefInstance {
	fnName := "<anonymous>"
	if fn.Name != nil {
		fnName = fn.Name.Name
	}
	w := &nilDerefWalker{
		ctx:     &nilDerefCtx{fset: fset, fnName: fnName},
		nilRisk: make(map[string]nilRiskEntry),
	}
	w.walk(fn.Body)
	return w.instances
}

// evalVisit inspects a subtree that cannot introduce new statements:
// it records dereferences and routes function literals back into the
// statement walker so closure bodies keep their own block scoping.
func (w *nilDerefWalker) evalVisit(n ast.Node) {
	if n == nil {
		return
	}
	ast.Inspect(n, func(node ast.Node) bool {
		if lit, ok := node.(*ast.FuncLit); ok {
			w.walk(lit.Body)
			return false
		}
		if inst := detectNilDeref(w.ctx, node, w.nilRisk); inst != nil {
			w.instances = append(w.instances, inst...)
		}
		return true
	})
}

// walkExpressionStatements handles statement kinds whose children are plain
// expressions or declarations - none of them contain nested blocks, so a
// single flat visit suffices for each. Includes the assignment risk
// bookkeeping from processAssignment. Returns whether st was recognized.
// (Extracted from walk during the #1127 rework.)
func (w *nilDerefWalker) walkExpressionStatements(st ast.Node) bool {
	switch s := st.(type) {
	case *ast.AssignStmt:
		processAssignment(s, w.nilRisk)
		// The old flat visitor descended through LHS expressions as
		// well (*p = v, cfg.hosts[i] = h); keep inspecting them.
		for _, lhs := range s.Lhs {
			w.evalVisit(lhs)
		}
		for _, rhs := range s.Rhs {
			w.evalVisit(rhs)
		}
	case *ast.ExprStmt:
		w.evalVisit(s.X)
	case *ast.SendStmt:
		w.evalVisit(s.Chan)
		w.evalVisit(s.Value)
	case *ast.IncDecStmt:
		w.evalVisit(s.X)
	case *ast.GoStmt:
		w.evalVisit(s.Call)
	case *ast.DeferStmt:
		w.evalVisit(s.Call)
	case *ast.ReturnStmt:
		for _, res := range s.Results {
			w.evalVisit(res)
		}
	case *ast.DeclStmt:
		w.evalVisit(s.Decl)
	default:
		return false
	}
	return true
}

// walk dispatches n recursively. Block statements get explicit enter/exit
// pairing, which a linear visitor cannot provide.
func (w *nilDerefWalker) walk(n ast.Node) {
	if n == nil {
		return
	}

	// #1127 (issue 1070 follow-up): implement block-scoped risk state
	// with explicit enter/exit pairing. Variables bound inside a nested
	// block are not visible outside it, so risk entries created inside
	// must not leak into following statements (the brother-block leak).
	// A defer placed inside the ast.Inspect callback fires when that
	// callback returns - not when the block ends - so the previous
	// depth counter never advanced and its clearing was dead code.
	// Mutations to entries that existed on entry (report-once deletions,
	// permanent clears from terminating #533 guards) deliberately
	// persist; only freshly added bindings are rolled back on exit.
	if blk, ok := n.(*ast.BlockStmt); ok {
		w.walkBlock(blk)
		return
	}

	// Error-check if statements get scoped handling (#238). Their Init
	// and branch pieces re-enter walk() and therefore inherit block
	// scoping automatically.
	if is, ok := n.(*ast.IfStmt); ok {
		walkErrorCheckIf(is, w.nilRisk, w.walk)
		return
	}

	if w.walkExpressionStatements(n) {
		return
	}

	w.walkCompound(n)
}

// walkCompound recurses into compound statements piece by piece in source
// order, routing condition/case expressions through evalVisit and their
// embedded blocks back through walk(). Unrecognized nodes fall back to
// evalVisit.
func (w *nilDerefWalker) walkCompound(n ast.Node) {
	switch s := n.(type) {
	case *ast.LabeledStmt:
		w.walk(s.Stmt)
	case *ast.ForStmt:
		w.walk(s.Init)
		w.evalVisit(s.Cond)
		w.walk(s.Post)
		w.walk(s.Body)
	case *ast.RangeStmt:
		w.evalVisit(s.Key)
		w.evalVisit(s.Value)
		w.evalVisit(s.X)
		w.walk(s.Body)
	case *ast.SwitchStmt:
		w.walk(s.Init)
		w.evalVisit(s.Tag)
		w.walk(s.Body)
	case *ast.TypeSwitchStmt:
		w.walk(s.Init)
		w.walk(s.Assign)
		w.walk(s.Body)
	case *ast.SelectStmt:
		w.walk(s.Body)
	case *ast.CaseClause:
		for _, e := range s.List {
			w.evalVisit(e)
		}
		for _, stmt := range s.Body {
			w.walk(stmt)
		}
	case *ast.CommClause:
		w.walk(s.Comm)
		for _, stmt := range s.Body {
			w.walk(stmt)
		}
	default:
		w.evalVisit(n)
	}
}

// walkBlock runs the statements of blk under block-scoped risk bookkeeping:
// entries bound inside the block are removed on exit; mutations to entries
// that already existed on entry persist (see walk for the full rationale).
func (w *nilDerefWalker) walkBlock(blk *ast.BlockStmt) {
	existed := make(map[string]bool, len(w.nilRisk))
	for name := range w.nilRisk {
		existed[name] = true
	}
	for _, st := range blk.List {
		w.walk(st)
	}
	for name := range w.nilRisk {
		if !existed[name] {
			delete(w.nilRisk, name)
		}
	}
}

// derefPathText renders the chain rooted at e as a position-independent
// token sequence used by the #1128 delta key. Traversal walks inward from
// the dereference site to its base; '/' separates levels. Recognized chain
// shapes continue inward; anything opaque contributes '?'.
func derefPathText(e ast.Expr) string {
	parts := make([]string, 0, 4)
	cur := e
	for depth := 0; cur != nil && depth < 64; depth++ {
		switch t := cur.(type) {
		case *ast.SelectorExpr:
			parts = append(parts, "."+t.Sel.Name)
			cur = t.X
		case *ast.IndexExpr:
			parts = append(parts, "[..]")
			cur = t.X
		case *ast.StarExpr:
			parts = append(parts, "*")
			cur = t.X
		case *ast.ParenExpr:
			cur = t.X
		case *ast.Ident:
			parts = append(parts, t.Name)
			cur = nil
		default:
			parts = append(parts, "?")
			cur = nil
		}
	}
	// Reverse into text order: base first, outer dereference last.
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "/")
}

// nilDerefKey builds the position-independent delta key (#1128): function
// name, normalized dereference path, variable name. Line numbers are
// excluded so inserting lines above cannot resurrect a suppressed warning.
// #1179: because the function name changes on rename/extraction, the delta
// filter also consults nilDerefSuffixKey as a fallback.
func nilDerefKey(ctx *nilDerefCtx, node ast.Expr, varName string) string {
	return ctx.fnName + "|" + derefPathText(node) + "|" + varName
}

// nilDerefSuffixKey builds the rename-tolerant fallback key (#1179): the
// normalized dereference path and variable name without any function-name
// component, so suppression survives a pure rename or extraction.
func nilDerefSuffixKey(node ast.Expr, varName string) string {
	return derefPathText(node) + "|" + varName
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

	// #1166: comma-ok assignments are not error-return assignments. In
	// `v, e := m[name]`, `v, e := x.(*T)` and `v, e := <-ch` the second
	// return value is a bool, so an error-named receiver does not make
	// the first value nil-risk. Skipping the mark also avoids the
	// un-clearable false positive: the advisory path only recognizes
	// `e == nil` / `e != nil` comparisons, never `if !e`.
	if isCommaOkRhs(assign) {
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

// isCommaOkRhs reports whether the assignment has the two-value comma-ok
// shape (#1166): exactly two receivers and a single RHS expression of the
// forms that yield (value, ok) - map index, type assertion, or channel
// receive. Parenthesized expressions are unwrapped.
func isCommaOkRhs(assign *ast.AssignStmt) bool {
	if len(assign.Lhs) != 2 || len(assign.Rhs) != 1 {
		return false
	}
	switch e := ast.Unparen(assign.Rhs[0]).(type) {
	case *ast.IndexExpr, *ast.TypeAssertExpr:
		return true
	case *ast.UnaryExpr:
		return e.Op == token.ARROW // channel receive
	}
	return false
}

// clearReassignedRisk permanently clears the nil risk of a variable that is
// reassigned alone (`v = ...`, #533) when the RHS is provably non-nil: an
// address-of expression (`&S{...}`) or a `new(T)` call. Assignment of `nil`,
// reads of the variable itself (`v = v.Next`), and ordinary calls keep the
// risk - they can still produce nil.
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
	case *ast.UnaryExpr: // &S{...} / &T{} - composite address, never nil
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
	case token.EQL: // if err == nil { ... } - value is safe inside the body
		suppressSaved()
		walk(is.Body)
		unsuppressSaved()
		if is.Else != nil { // else implies err != nil: risk applies
			walk(is.Else)
		}
	case token.NEQ: // if err != nil { ... } - value is still at risk inside
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

// detectNilDeref checks if a node dereferences a nil-risk variable. The
// per-function ctx supplies both the file set and the enclosing function
// name used to build the #1128 delta key.
func detectNilDeref(ctx *nilDerefCtx, n ast.Node, nilRisk map[string]nilRiskEntry) []nilDerefInstance {
	fset := ctx.fset
	var instances []nilDerefInstance

	switch node := n.(type) {
	case *ast.SelectorExpr:
		// x.Field or x.Method()
		if x, ok := node.X.(*ast.Ident); ok {
			if entry, risk := nilRisk[x.Name]; risk && !entry.cleared {
				pos := fset.Position(node.Pos())
				instances = append(instances, nilDerefInstance{
					// #1069/#1128: posStr stays line-anchored for display;
					// the position-independent key prevents re-reporting
					// after insertions above (#1128).
					posStr:    fmt.Sprintf("%s:%d:%s", filepath.Base(pos.Filename), pos.Line, x.Name),
					key:       nilDerefKey(ctx, node.X, x.Name),
					suffixKey: nilDerefSuffixKey(node.X, x.Name),
					varName:   x.Name,
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
					// #1128: same key scheme as SelectorExpr.
					posStr:    fmt.Sprintf("%s:%d:%s", filepath.Base(pos.Filename), pos.Line, x.Name),
					key:       nilDerefKey(ctx, node.X, x.Name),
					suffixKey: nilDerefSuffixKey(node.X, x.Name),
					varName:   x.Name,
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
					// #1128: same key scheme as SelectorExpr.
					posStr:    fmt.Sprintf("%s:%d:%s", filepath.Base(pos.Filename), pos.Line, x.Name),
					key:       nilDerefKey(ctx, node.X, x.Name),
					suffixKey: nilDerefSuffixKey(node.X, x.Name),
					varName:   x.Name,
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
