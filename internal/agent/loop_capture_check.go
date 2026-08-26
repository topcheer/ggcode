package agent

// Range/For Loop Variable Capture in Goroutines and Deferred Closures
//
// Problem: AI coding agents frequently produce Go code with loop variable
// capture bugs - one of the most classic and dangerous Go pitfalls.
//
// Before Go 1.22, loop variables (both for-init and range) are shared across
// all iterations. Capturing them in a closure launched via go func() or
// defer func() causes all closures to see the last value:
//
//	for _, item := range items {
//	    go func() {
//	        process(item) // BUG: all goroutines see the last item
//	    }()
//	}
//
// In Go 1.22+ both range and for-init loop variables are per-iteration
// (#1108: for-init coverage follows the Go 1.22 release notes), so the
// shared-variable warning only applies when targeting older Go versions.
// Additionally, the code may be ported to older Go versions, making this a
// latent bug.
//
// Safe patterns (not flagged):
//   - Passing as parameter: go func(item T) { process(item) }(item)
//   - Rebinding: item := item before the go statement
//
// Competitor analysis:
//   - Claude Code: no write-time detection
//   - Cursor: no detection (staticcheck S1011 is unrelated)
//   - Cline/OpenHands: no detection
//   - Aider: no detection
//   - Devin: no detection
//   - go vet: does NOT detect this (no closure capture analysis)
//
// Approach: AST-based analysis. For each for/range loop, collect loop variable
// names, then check if any go func() { ... }() or defer func() { ... }()
// inside the loop body references the loop variable without passing it as a
// parameter. Delta-aware: only flags patterns newly introduced by this edit.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// Issue #1100: read go.mod to detect Go version for range loop semantics.
// Go 1.22+ uses per-iteration range variables, so the "classic gotcha" warning
// should be suppressed for range loops when using Go 1.22+.
var goModVersionChecked bool
var isGo122Plus bool

func checkGoModVersion() {
	if goModVersionChecked {
		return
	}
	goModVersionChecked = true

	// Read go.mod file in the current directory or parent directories.
	// #1108: start from the absolute working dir - filepath.Dir(".") == "."
	// terminates the walk immediately, so the upward search only worked
	// when cwd happened to be the module root.
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for {
		path := filepath.Join(dir, "go.mod")
		if content, err := os.ReadFile(path); err == nil {
			// Parse go version line (e.g., "go 1.22.0" or "go 1.26.2")
			lines := strings.Split(string(content), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "go ") {
					version := strings.TrimPrefix(line, "go ")
					parts := strings.Split(version, ".")
					if len(parts) >= 2 {
						major := strings.TrimSpace(parts[0])
						minor := strings.TrimSpace(parts[1])
						if major == "1" {
							// Compare minor version: 22+ means Go 1.22+
							if len(minor) > 0 && minor[0] >= '2' && len(minor) >= 2 && minor[0:2] >= "22" {
								isGo122Plus = true
							}
						} else if major >= "2" {
							// Go 2.0+ definitely has per-iteration range variables
							isGo122Plus = true
						}
					}
					break
				}
			}
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
}

// loopCaptureInstance represents a detected loop variable capture issue.
// Issue #1100: added funcName for finer-grained delta key to avoid collapsing
// multiple capture points into one warning.
type loopCaptureInstance struct {
	posStr   string // position of the function literal
	varName  string // captured loop variable name
	loopType string // "range" or "for"
	kind     string // "goroutine" or "defer"
	funcName string // containing function name for delta key
}

// checkLoopVarCapture performs AST-based loop variable capture detection on
// Go source. Returns warnings for newly-introduced capture patterns.
func checkLoopVarCapture(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	if strings.HasSuffix(filePath, "_test.go") {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	// Issue #1100: check go.mod version to suppress range warnings for Go 1.22+
	checkGoModVersion()

	oldSet := collectLoopCaptureIssues(oldContent)
	newInstances := findLoopCaptureIssues(newContent)

	var warnings []string
	for _, inst := range newInstances {
		// Issue #1100: use funcName+varName+kind+loopType as delta key
		// instead of just varName+kind+loopType to avoid collapsing
		// multiple capture points in the same function.
		key := inst.funcName + "|" + inst.varName + "|" + inst.kind + "|" + inst.loopType
		if oldSet[key] {
			continue
		}
		warnings = append(warnings, formatLoopCaptureWarning(inst))
	}

	if len(warnings) > 3 {
		warnings = warnings[:3]
	}
	return warnings
}

// formatLoopCaptureWarning converts a loopCaptureInstance into a warning string.
// Issue #1100: for range loops with Go 1.22+, the warning is downgraded
// since range variables are per-iteration.
func formatLoopCaptureWarning(inst loopCaptureInstance) string {
	fixHint := " Pass it as a parameter: go func(item T) { use(item) }(item), or rebind: item := item before the closure."
	if inst.kind == "goroutine" {
		// Issue #1100/#1108: suppress "classic gotcha" warning for loop
		// captures in Go 1.22+ - per-iteration semantics cover BOTH range
		// variables and for-init declared variables per the Go 1.22 release
		// notes ("both those declared by the init statement and those
		// declared by range").
		rangeWarning := "all goroutines may see the last iteration's value (classic Go gotcha)."
		if isGo122Plus {
			rangeWarning = "may still be a bug if the closure is delayed or saved for later use."
		}
		return fmt.Sprintf(
			"Loop variable '%s' captured in goroutine at %s inside %s loop: "+
				"%s%s",
			inst.varName, inst.posStr, inst.loopType, rangeWarning, fixHint)
	}
	return fmt.Sprintf(
		"Loop variable '%s' captured in deferred closure at %s inside %s loop: "+
			"the deferred call will use the variable's final value, not the value at defer time."+
			"%s",
		inst.varName, inst.posStr, inst.loopType, fixHint)
}

// findLoopCaptureIssues parses Go source and returns all loop capture issues.
func findLoopCaptureIssues(src string) []loopCaptureInstance {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
	if err != nil || file == nil {
		return nil
	}

	var instances []loopCaptureInstance
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		funcName := fn.Name.Name
		instances = append(instances, analyzeLoopsForCapture(fset, funcName, fn.Body)...)
	}
	return instances
}

// analyzeLoopsForCapture walks a function body looking for for/range loops
// and checks their bodies for loop variable capture in closures.
// Issue #1100: added funcName parameter for delta key generation.
func analyzeLoopsForCapture(fset *token.FileSet, funcName string, body *ast.BlockStmt) []loopCaptureInstance {
	var instances []loopCaptureInstance

	ast.Inspect(body, func(node ast.Node) bool {
		var loopVars []string
		var loopBody *ast.BlockStmt
		var loopType string

		switch n := node.(type) {
		case *ast.RangeStmt:
			loopVars = rangeLoopVars(n)
			loopBody = n.Body
			loopType = "range"
		case *ast.ForStmt:
			loopVars = forLoopVars(n)
			loopBody = n.Body
			loopType = "for"
		default:
			return true
		}

		if len(loopVars) == 0 || loopBody == nil {
			return true
		}

		rebound := collectReboundVars(loopBody, loopVars)
		for _, v := range loopVars {
			if rebound[v] {
				continue
			}
			instances = append(instances, findCaptureInBody(fset, funcName, loopBody, v, loopType)...)
		}
		return true
	})

	return instances
}

// rangeLoopVars extracts loop variable names from a RangeStmt.
func rangeLoopVars(rs *ast.RangeStmt) []string {
	var vars []string
	if id, ok := rs.Key.(*ast.Ident); ok && id.Name != "_" {
		vars = append(vars, id.Name)
	}
	if id, ok := rs.Value.(*ast.Ident); ok && id.Name != "_" {
		vars = append(vars, id.Name)
	}
	return vars
}

// forLoopVars extracts loop variable names from a ForStmt init clause.
func forLoopVars(fs *ast.ForStmt) []string {
	if fs.Init == nil {
		return nil
	}
	as, ok := fs.Init.(*ast.AssignStmt)
	if !ok {
		return nil
	}
	var vars []string
	for _, lhs := range as.Lhs {
		if id, ok := lhs.(*ast.Ident); ok && id.Name != "_" {
			vars = append(vars, id.Name)
		}
	}
	return vars
}

// collectReboundVars detects v := v rebindings at the top level of a loop
// body. If a loop variable is rebound before use, captures of it are safe.
func collectReboundVars(body *ast.BlockStmt, loopVars []string) map[string]bool {
	target := make(map[string]bool)
	for _, v := range loopVars {
		target[v] = true
	}
	result := make(map[string]bool)
	for _, stmt := range body.List {
		as, ok := stmt.(*ast.AssignStmt)
		if !ok || as.Tok != token.DEFINE {
			continue
		}
		scanRebindAssignment(as, target, result)
	}
	return result
}

// scanRebindAssignment checks a single assignment for v := v rebinding.
func scanRebindAssignment(as *ast.AssignStmt, target, result map[string]bool) {
	for i, lhs := range as.Lhs {
		if i >= len(as.Rhs) {
			return
		}
		lhsID, ok := lhs.(*ast.Ident)
		if !ok {
			continue
		}
		rhsID, ok := as.Rhs[i].(*ast.Ident)
		if !ok {
			continue
		}
		if lhsID.Name == rhsID.Name && target[lhsID.Name] {
			result[lhsID.Name] = true
		}
	}
}

// findCaptureInBody searches a loop body for go/defer function literals that
// capture the given loop variable without passing it as a parameter.
// Issue #1100: added funcName parameter for delta key generation.
func findCaptureInBody(fset *token.FileSet, funcName string, body *ast.BlockStmt, varName, loopType string) []loopCaptureInstance {
	var instances []loopCaptureInstance

	ast.Inspect(body, func(node ast.Node) bool {
		fl, kind := extractCaptureTarget(node)
		if fl == nil {
			return true
		}

		// Safe if the variable is passed as a parameter.
		if collectFuncLitParams(fl)[varName] {
			return true
		}

		// Flag if the variable is referenced inside the closure body.
		if identReferenced(fl.Body, varName) {
			instances = append(instances, loopCaptureInstance{
				posStr:   fset.Position(fl.Pos()).String(),
				varName:  varName,
				loopType: loopType,
				kind:     kind,
				funcName: funcName,
			})
		}
		return true
	})

	return instances
}

// extractCaptureTarget checks if a node is a go/defer call wrapping a FuncLit.
// Returns the FuncLit and the kind ("goroutine" or "defer"), or nil/"".
func extractCaptureTarget(node ast.Node) (*ast.FuncLit, string) {
	switch n := node.(type) {
	case *ast.GoStmt:
		if fl, ok := n.Call.Fun.(*ast.FuncLit); ok {
			return fl, "goroutine"
		}
	case *ast.DeferStmt:
		if fl, ok := n.Call.Fun.(*ast.FuncLit); ok {
			return fl, "defer"
		}
	}
	return nil, ""
}

// collectFuncLitParams returns parameter names declared in a function literal.
func collectFuncLitParams(fl *ast.FuncLit) map[string]bool {
	result := make(map[string]bool)
	if fl.Type == nil || fl.Type.Params == nil {
		return result
	}
	for _, field := range fl.Type.Params.List {
		for _, name := range field.Names {
			result[name.Name] = true
		}
	}
	return result
}

// identReferenced checks if an identifier with the given name appears anywhere
// in the AST subtree.
func identReferenced(node ast.Node, name string) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
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

// collectLoopCaptureIssues parses old content and returns a set of existing
// loop capture issue signatures for delta-aware suppression.
// Issue #1100: use funcName+varName+kind+loopType as delta key.
func collectLoopCaptureIssues(src string) map[string]bool {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	instances := findLoopCaptureIssues(src)
	result := make(map[string]bool, len(instances))
	for _, inst := range instances {
		// Issue #1100: use funcName+varName+kind+loopType as delta key
		key := inst.funcName + "|" + inst.varName + "|" + inst.kind + "|" + inst.loopType
		result[key] = true
	}
	return result
}
