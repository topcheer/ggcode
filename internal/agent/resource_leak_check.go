package agent

// Resource Leak Detection in Go Code
//
// Problem: AI coding agents (and human developers) frequently produce Go code
// that acquires resources -- file handles, HTTP response bodies, mutex locks,
// network listeners -- without the corresponding defer cleanup call. This
// causes file descriptor exhaustion, goroutine leaks, deadlocks, and memory
// pressure that manifest as intermittent production failures.
//
// LLMs are especially prone to this because they focus on the happy-path logic
// and frequently omit the defer statement that a careful human would add
// immediately after the resource acquisition.
//
// Competitor analysis:
//   - Claude Code: no automatic detection (relies on external linters)
//   - Cursor: no automatic detection (lint-on-save may catch via golangci-lint)
//   - Cline/OpenHands: reactive only -- caught by tests or production incidents
//   - Aider: no automatic detection
//   - Windsurf: no automatic detection
//   - GitHub Copilot: sometimes warns via lint integration
//
// None provide INLINE detection at write time. External tools (staticcheck,
// errcheck, revive) can catch some of these, but require a separate lint cycle
// and are not always installed. This check provides immediate, zero-dependency
// feedback in <1ms per file using Go's standard library AST parser.
//
// Approach: AST-based analysis of Go functions. For each resource-acquiring
// call pattern, verify that a corresponding cleanup call exists in the same
// function body. Only unambiguous patterns are checked to minimize false
// positives:
//  1. os.Open / os.Create / os.OpenFile -> defer f.Close()
//  2. http.Get / http.Post / http.Head -> defer resp.Body.Close()
//  3. net.Listen -> defer l.Close()

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"strings"
)

// resourceAcquisition defines a function call that creates a resource
// requiring cleanup.
type resourceAcquisition struct {
	pkg           string   // package qualifier (e.g., "os", "net", "http")
	funcs         []string // function names that acquire the resource
	resourceHint  string   // description for the warning message
	cleanupMethod string   // expected cleanup method name (e.g., "Close")
	bodyField     bool     // if true, track .Body sub-field (http responses)
}

// resourceAcquisitions lists the resource-acquiring call patterns we check.
var resourceAcquisitions = []resourceAcquisition{
	{
		pkg:           "os",
		funcs:         []string{"Open", "Create", "OpenFile"},
		resourceHint:  "file handle",
		cleanupMethod: "Close",
	},
	{
		pkg:           "net",
		funcs:         []string{"Listen"},
		resourceHint:  "network listener",
		cleanupMethod: "Close",
	},
	{
		pkg:           "http",
		funcs:         []string{"Get", "Post", "Head", "PostForm"},
		resourceHint:  "HTTP response body",
		cleanupMethod: "Close",
		bodyField:     true,
	},
}

// resourceLeak represents a detected resource acquisition without cleanup.
type resourceLeak struct {
	varName      string
	resourceHint string
	pos          token.Pos
	sig          string // normalized source text of the acquisition call (#1192)
}

// resourceLeakDeltaIndex holds one consumable suppression token per leak
// present in the old content (#1192, modeled on nilDerefDeltaIndex/#1186):
// exact-key first, rename-tolerant suffix key as fallback, and each old token
// suppresses at most one new instance so a set of same-shape tokens can never
// absorb a genuinely new leak in a different function.
type resourceLeakDeltaIndex struct {
	tokens []oldResourceLeakToken
}

// oldResourceLeakToken is one consumable suppression token for a pre-existing
// leak (#1192).
type oldResourceLeakToken struct {
	key       string // fnName|varName (exact)
	suffixKey string // acquisitionCallSig|varName (fnName-independent)
	oldFnName string // to verify the old name vanished on fallback match
	used      bool
}

// matchOldResourceLeak consumes one unconsumed old token for the given new
// leak: prefer the exact fnName|varName key; fall back to the rename-tolerant
// suffix key only when the token's old function name no longer exists in the
// new file (pure function rename, #1192). Returns false when no token remains.
func (idx *resourceLeakDeltaIndex) matchOldResourceLeak(inst resourceLeak, fnName string, newFnNames map[string]bool) bool {
	exact := fnName + "|" + inst.varName
	for i := range idx.tokens {
		t := &idx.tokens[i]
		if !t.used && t.key == exact {
			t.used = true
			return true
		}
	}
	suffix := inst.sig + "|" + inst.varName
	for i := range idx.tokens {
		t := &idx.tokens[i]
		if t.used || t.suffixKey != suffix {
			continue
		}
		// Rename fallback (#1192): the old function name must have disappeared
		// from the new file; if it still exists, this is a new same-shape
		// instance elsewhere and must be reported.
		if newFnNames[t.oldFnName] {
			continue
		}
		t.used = true
		return true
	}
	return false
}

// collectOldResourceLeakIndex parses oldContent and returns one token per
// leak present before the edit (#1192). Uses the same exemption rules
// (cleanup + ownership transfer) as the new-content pass for consistency.
func collectOldResourceLeakIndex(filePath, oldContent string) resourceLeakDeltaIndex {
	idx := resourceLeakDeltaIndex{}
	if strings.TrimSpace(oldContent) == "" {
		return idx
	}
	oldFset := token.NewFileSet()
	oldFile, oldErr := parser.ParseFile(oldFset, filePath, oldContent, 0)
	if oldErr != nil || oldFile == nil {
		return idx
	}
	for _, decl := range oldFile.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		acquired := findResourceAcquisitions(fn)
		if len(acquired) == 0 {
			continue
		}
		fnName := "<anonymous>"
		if fn.Name != nil {
			fnName = fn.Name.Name
		}
		cleanedVars := findCleanupCalls(fn)
		for _, acq := range acquired {
			if _, cleaned := cleanedVars[acq.varName]; cleaned {
				continue
			}
			if ownershipTransferred(fn, acq.varName) {
				continue
			}
			idx.tokens = append(idx.tokens, oldResourceLeakToken{
				key:       fnName + "|" + acq.varName,
				suffixKey: acq.sig + "|" + acq.varName,
				oldFnName: fnName,
			})
		}
	}
	return idx
}

// checkResourceLeaks performs AST-based resource leak detection on Go source.
// Returns warnings for resources acquired without corresponding cleanup calls.
// Delta-aware: parses oldContent and suppresses pre-existing (unchanged)
// acquisitions so unrelated edits do not re-warn and squeeze out the single
// maxIntegrityWarnings slot (#221). Rename-tolerant since #1192: a pure
// function rename no longer re-reports an existing leak.
//
// Parameters:
//   - filePath: path of the written file (used for language detection)
//   - oldContent: the file content before the write ("" for new files)
//   - src: the file content after the write
func checkResourceLeaks(filePath, oldContent, src string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.TrimSpace(src) == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, src, 0)
	if err != nil || file == nil {
		return nil
	}

	// Delta: collect one consumable token per pre-existing leak (#221, #1192).
	oldIdx := collectOldResourceLeakIndex(filePath, oldContent)

	// Function names present in the new content: the rename fallback must
	// only fire when the old function name has disappeared (#1192).
	newFnNames := map[string]bool{}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name != nil {
			newFnNames[fn.Name.Name] = true
		}
	}

	var warnings []string

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		warnings = append(warnings, newResourceLeakWarnings(fn, fset, &oldIdx, newFnNames)...)
	}

	return warnings
}

// newResourceLeakWarnings flags resources acquired in fn that have no cleanup
// call, no ownership transfer (#1191), and no matching pre-existing token in
// the delta index (#1192).
func newResourceLeakWarnings(fn *ast.FuncDecl, fset *token.FileSet, oldIdx *resourceLeakDeltaIndex, newFnNames map[string]bool) []string {
	acquired := findResourceAcquisitions(fn)
	if len(acquired) == 0 {
		return nil
	}

	fnName := "<anonymous>"
	if fn.Name != nil {
		fnName = fn.Name.Name
	}

	var warnings []string
	cleanedVars := findCleanupCalls(fn)
	for _, acq := range acquired {
		if _, cleaned := cleanedVars[acq.varName]; cleaned {
			continue
		}
		// #1191: acquire-and-return / ownership-transfer idiom -- the
		// caller (or callee) owns the resource and closes it.
		if ownershipTransferred(fn, acq.varName) {
			continue
		}
		if oldIdx.matchOldResourceLeak(acq, fnName, newFnNames) {
			continue // pre-existing leak (possibly renamed), already warned
		}
		warnings = append(warnings, fmt.Sprintf(
			"Possible resource leak: %s acquired (variable %s at %s) but no defer .Close() "+
				"or cleanup call found in the same function. Verify ownership: if this function "+
				"retains the resource, add `defer %s.Close()` immediately after the assignment; "+
				"if %s is returned or handed off to a caller/callee, that side is responsible for "+
				"closing it.",
			acq.resourceHint, acq.varName, fset.Position(acq.pos), acq.varName, acq.varName))
	}
	return warnings
}

// findResourceAcquisitions walks a function body and finds resource-acquiring
// calls, returning the variable names they are assigned to.
func findResourceAcquisitions(fn *ast.FuncDecl) []resourceLeak {
	var leaks []resourceLeak

	ast.Inspect(fn.Body, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}

		for i, expr := range assign.Rhs {
			call, ok := expr.(*ast.CallExpr)
			if !ok {
				continue
			}

			acq := matchResourceCall(call)
			if acq == nil {
				continue
			}

			if i >= len(assign.Lhs) {
				continue
			}

			varName := extractVarName(assign.Lhs[i], acq.bodyField)
			if varName == "" {
				continue
			}

			leaks = append(leaks, resourceLeak{
				varName:      varName,
				resourceHint: acq.resourceHint,
				pos:          call.Pos(),
				sig:          nodeSignature(call),
			})
		}
		return true
	})

	return leaks
}

// nodeSignature renders an AST node back to normalized source text; identical
// acquisition calls produce identical signatures regardless of surrounding
// formatting or enclosing function name (#1192 rename-tolerant delta key).
func nodeSignature(n ast.Node) string {
	var b strings.Builder
	if err := printer.Fprint(&b, token.NewFileSet(), n); err != nil {
		return ""
	}
	return b.String()
}

// ownershipTransferred reports whether the resource variable escapes the
// function (#1191): it appears in any return statement expression, or is
// passed as an argument to another function call (acquire-and-return /
// acquire-and-hand-off idiom). In both cases the caller or callee owns the
// resource and is responsible for closing it.
func ownershipTransferred(fn *ast.FuncDecl, varName string) bool {
	base := varName
	if i := strings.Index(base, "."); i >= 0 {
		base = base[:i]
	}
	transferred := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if transferred {
			return false
		}
		switch node := n.(type) {
		case *ast.ReturnStmt:
			for _, res := range node.Results {
				if exprReferencesIdent(res, base) {
					transferred = true
					return false
				}
			}
		case *ast.CallExpr:
			for _, arg := range node.Args {
				if exprReferencesIdent(arg, base) {
					transferred = true
					return false
				}
			}
		}
		return true
	})
	return transferred
}

// exprReferencesIdent reports whether e contains an identifier with the given
// name (used to detect the resource variable escaping via return or call
// argument, #1191).
func exprReferencesIdent(e ast.Expr, name string) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if found {
			return false
		}
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = true
			return false
		}
		return true
	})
	return found
}

// matchResourceCall checks if a call expression matches a known resource
// acquisition pattern (e.g., os.Open, http.Get).
func matchResourceCall(call *ast.CallExpr) *resourceAcquisition {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil
	}

	for i := range resourceAcquisitions {
		acq := &resourceAcquisitions[i]
		if pkgIdent.Name != acq.pkg {
			continue
		}
		for _, fn := range acq.funcs {
			if sel.Sel.Name == fn {
				return acq
			}
		}
	}
	return nil
}

// extractVarName gets the variable name from an assignment LHS expression.
// For bodyField=true (http.Get), it extracts the receiver.Body accessor.
func extractVarName(expr ast.Expr, bodyField bool) string {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return ""
	}
	if bodyField {
		return ident.Name + ".Body"
	}
	return ident.Name
}

// findCleanupCalls walks a function body and returns a set of variable names
// that have a Close (or similar cleanup) method called on them.
func findCleanupCalls(fn *ast.FuncDecl) map[string]bool {
	cleaned := make(map[string]bool)

	ast.Inspect(fn.Body, func(node ast.Node) bool {
		var call *ast.CallExpr

		if d, ok := node.(*ast.DeferStmt); ok {
			call = d.Call
		}
		if c, ok := node.(*ast.CallExpr); ok {
			call = c
		}

		if call == nil {
			return true
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		// Only count calls to cleanup-like methods (Close, CloseAll, etc.)
		// This avoids treating f.Read() as a cleanup for f.
		if !isCleanupMethod(sel.Sel.Name) {
			return true
		}

		var varName string
		switch x := sel.X.(type) {
		case *ast.Ident:
			varName = x.Name
		case *ast.SelectorExpr:
			if inner, ok := x.X.(*ast.Ident); ok {
				varName = inner.Name + "." + x.Sel.Name
			}
		}

		if varName != "" {
			cleaned[varName] = true
		}

		return true
	})

	return cleaned
}

// cleanupMethods lists method names that constitute resource cleanup.
var cleanupMethods = map[string]bool{
	"Close":    true,
	"CloseAll": true,
	"Cleanup":  true,
	"Release":  true,
	"Free":     true,
	"Shutdown": true,
	"Stop":     true,
	"Unlock":   true,
	"RUnlock":  true,
}

// isCleanupMethod returns true if the method name is a known cleanup method.
func isCleanupMethod(name string) bool {
	return cleanupMethods[name]
}
