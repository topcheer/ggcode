package agent

// Post-Write Preallocation Detection (Check #56)
//
// Trend: Performance & Latency Optimization - Memory Allocation Hotspot Detection
//
// Problem: AI coding agents frequently generate code that appends to slices
// inside loops without preallocating capacity. Each append beyond capacity
// triggers a full copy (O(n) allocation), so N iterations cause O(N log N)
// total allocations and copies. For N=10000, this can mean ~14 reallocation
// events and ~150MB of churned memory, causing GC pressure and latency spikes.
//
// The fix is trivial: make([]T, 0, estimatedCapacity) eliminates all
// intermediate allocations. This is the #1 allocation optimization in Go
// performance tuning (covered in "High Performance Go" and Dave Cheney's talks).
//
// Competitor analysis:
//   - Claude Code: no detection
//   - Cursor: relies on staticcheck SA1024 (only for make([]T, 0) vs make([]T, 0, 0))
//   - Cline/OpenHands: no detection
//   - Aider: no detection
//   - go vet: does not flag append-in-loop without preallocation
//   - staticcheck: does not flag this pattern
//   - prealloc linter: catches this but is NOT in default toolchains
//   - gocritic: has prealloc check but rarely configured
//
// Detection approach: AST-based, delta-aware. Find for/range loops with
// append() calls to slice variables declared as `var x []T` or `x := []T{}`
// (zero-length, zero-capacity). Exclude cases where:
//   - The slice is already preallocated via make([]T, 0, n)
//   - The loop has a data-dependent early break (capacity unknown)
//   - The append target is a function call result (e.g., x = foo(x))

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// preallocWarning represents a single preallocation warning.
type preallocWarning struct {
	pos      token.Pos
	varName  string
	loopLine int
}

func (w preallocWarning) String() string {
	return fmt.Sprintf("slice %q appended in loop without preallocation", w.varName)
}

// checkMissingPrealloc detects slices that are appended to inside loops
// without being preallocated with make([]T, 0, capacity).
func checkMissingPrealloc(filePath, oldContent, newContent string) []string {
	if !strings.HasSuffix(filePath, ".go") {
		return nil
	}
	if strings.HasSuffix(filePath, "_test.go") {
		return nil // tests often have small slices, not worth flagging
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	fset := token.NewFileSet()
	newAST, err := parser.ParseFile(fset, filePath, newContent, 0)
	if err != nil {
		return nil // syntax errors handled by other checks
	}

	// Find prealloc patterns in new content.
	newPatterns := findMissingPrealloc(newAST, fset)
	if len(newPatterns) == 0 {
		return nil
	}

	// Delta: subtract patterns already present in old content.
	if strings.TrimSpace(oldContent) != "" {
		oldAST, _ := parser.ParseFile(token.NewFileSet(), filePath, oldContent, 0)
		if oldAST != nil {
			oldPatterns := findMissingPrealloc(oldAST, token.NewFileSet())
			if len(oldPatterns) > 0 {
				oldSet := make(map[string]bool)
				for _, p := range oldPatterns {
					oldSet[p.String()] = true
				}
				var delta []preallocWarning
				for _, p := range newPatterns {
					if !oldSet[p.String()] {
						delta = append(delta, p)
					}
				}
				newPatterns = delta
			}
		}
	}

	if len(newPatterns) == 0 {
		return nil
	}

	var warnings []string
	for i, p := range newPatterns {
		if i >= 3 { // cap at 3 to avoid flooding
			warnings = append(warnings, fmt.Sprintf("...and %d more preallocation warning(s)", len(newPatterns)-3))
			break
		}
		warnings = append(warnings, fmt.Sprintf(
			"Missing preallocation: slice %q appended in loop at %s:%d without make([]T, 0, N). "+
				"Each append beyond capacity triggers a full copy (O(N log N) total allocations for N iterations). "+
				"Pre-allocate with make([]T, 0, expectedLen) to eliminate intermediate allocations and reduce GC pressure.",
			p.varName, filepath.Base(filePath), p.loopLine))
	}
	return warnings
}

// zeroCapSliceDecl tracks slices declared without capacity (zero-length, zero-cap).
type zeroCapSliceDecl struct {
	name string
	pos  token.Pos
	// hasMake tracks if the slice was created via make() with a capacity hint.
	hasMakeCapacity bool
}

// findMissingPrealloc scans the AST for append-in-loop without preallocation.
// #1103: declaration collection and loop scanning are correlated per
// function unit (top-level FuncDecl or nested FuncLit). A unit only ever
// consults declarations visible from that unit - its own locals plus
// package-level vars - so a same-named slice appended in one function can no
// longer satisfy an unrelated loop's append in another function.
func findMissingPrealloc(file *ast.File, fset *token.FileSet) []preallocWarning {
	pkgDecls := make(map[string]*zeroCapSliceDecl)
	for _, vs := range packageVarSpecs(file) {
		addValueSpecSliceDecls(vs, pkgDecls)
	}

	var warnings []preallocWarning
	seenLoopWarn := make(map[string]bool)

	scanUnit := func(body *ast.BlockStmt) {
		// Visible declarations: package-level zero-cap slices plus this
		// unit's own local declarations.
		decls := make(map[string]*zeroCapSliceDecl, len(pkgDecls)+4)
		for k, v := range pkgDecls {
			decls[k] = v
		}
		collectFuncSliceDecls(body, decls)
		if len(decls) == 0 {
			return
		}

		unitWarned := make(map[string]bool)
		onLoop := func(loopBody *ast.BlockStmt, loopPos token.Pos) {
			loopLine := fset.Position(loopPos).Line
			for _, w := range scanForAppend(loopBody, decls, loopLine) {
				key := fmt.Sprintf("%d:%s", loopLine, w.varName)
				if unitWarned[w.varName] || seenLoopWarn[key] {
					continue // one warning per variable per unit and per loop site
				}
				unitWarned[w.varName] = true
				seenLoopWarn[key] = true
				warnings = append(warnings, w)
			}
		}
		aSTInspectLoops(body, onLoop)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch fn := n.(type) {
		case *ast.FuncDecl:
			if fn.Body != nil {
				scanUnit(fn.Body)
			}
		case *ast.FuncLit:
			if fn.Body != nil {
				scanUnit(fn.Body) // closures form their own unit (#1103)
			}
		}
		return true
	})

	return warnings
}

// packageVarSpecs returns the ValueSpecs of package-level var declarations.
func packageVarSpecs(file *ast.File) []*ast.ValueSpec {
	var specs []*ast.ValueSpec
	for _, d := range file.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			if vs, ok := spec.(*ast.ValueSpec); ok {
				specs = append(specs, vs)
			}
		}
	}
	return specs
}

// addValueSpecSliceDecls records zero-capacity slice declarations found in a
// single value spec into decls.
func addValueSpecSliceDecls(vs *ast.ValueSpec, decls map[string]*zeroCapSliceDecl) {
	for i, name := range vs.Names {
		if vs.Type != nil {
			// var x []T - check if type is a slice.
			if _, isSlice := vs.Type.(*ast.ArrayType); isSlice {
				if i < len(vs.Values) {
					decls[name.Name] = analyzeInitExpr(name.Name, name.Pos(), vs.Values[i])
				} else {
					decls[name.Name] = &zeroCapSliceDecl{
						name: name.Name, pos: name.Pos(), hasMakeCapacity: false,
					}
				}
			}
		} else if i < len(vs.Values) {
			if d := analyzeInitExpr(name.Name, name.Pos(), vs.Values[i]); d != nil {
				decls[name.Name] = d
			}
		}
	}
}

// collectFuncSliceDecls records zero-capacity slice declarations local to a
// function unit body: function-local var GenDecls and short defines.
// Nested function literals are pruned - closures are analyzed as their own
// units so their internal names never leak into the enclosing scope (#1103).
func collectFuncSliceDecls(body *ast.BlockStmt, decls map[string]*zeroCapSliceDecl) {
	if body == nil {
		return
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.GenDecl:
			if node.Tok != token.VAR {
				return true
			}
			for _, spec := range node.Specs {
				if vs, ok := spec.(*ast.ValueSpec); ok {
					addValueSpecSliceDecls(vs, decls)
				}
			}
		case *ast.AssignStmt:
			if node.Tok != token.DEFINE {
				return true
			}
			for i, lhs := range node.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok || i >= len(node.Rhs) {
					continue
				}
				if d := analyzeInitExpr(ident.Name, ident.Pos(), node.Rhs[i]); d != nil {
					decls[ident.Name] = d
				}
			}
		}
		return true
	})
}

// aSTInspectLoops invokes fn for every for/range loop lexically inside body,
// including loops located in nested closures.
func aSTInspectLoops(body *ast.BlockStmt, fn func(loopBody *ast.BlockStmt, loopPos token.Pos)) {
	ast.Inspect(body, func(n ast.Node) bool {
		switch loop := n.(type) {
		case *ast.ForStmt:
			if loop.Body != nil {
				fn(loop.Body, loop.Pos())
			}
		case *ast.RangeStmt:
			if loop.Body != nil {
				fn(loop.Body, loop.Pos())
			}
		}
		return true
	})
}

// scanForAppend walks a statement list looking for append() calls assigned
// to zero-capacity slice declarations.

// analyzeInitExpr determines if an initialization expression produces a
// zero-capacity slice. Returns nil if the expression is not a slice.
func analyzeInitExpr(name string, pos token.Pos, expr ast.Expr) *zeroCapSliceDecl {
	// Composite literal: []T{} or []T{...}
	if cl, ok := expr.(*ast.CompositeLit); ok {
		if _, isSlice := cl.Type.(*ast.ArrayType); isSlice {
			return &zeroCapSliceDecl{name: name, pos: pos, hasMakeCapacity: false}
		}
		return nil
	}
	// make() call: make([]T, len) or make([]T, len, cap)
	if call, ok := expr.(*ast.CallExpr); ok {
		if fun, ok := call.Fun.(*ast.Ident); ok && fun.Name == "make" {
			// Check first arg is a slice type.
			if len(call.Args) > 0 {
				if _, isSlice := call.Args[0].(*ast.ArrayType); isSlice {
					// make([]T) - zero cap, but unusual.
					// make([]T, n) - has capacity hint (n).
					// make([]T, 0, cap) - explicitly has cap.
					hasCap := len(call.Args) >= 3
					if !hasCap && len(call.Args) == 2 {
						// make([]T, n) - if n is a literal > 0, it has capacity.
						if lit, ok := call.Args[1].(*ast.BasicLit); ok {
							if lit.Value != "0" {
								hasCap = true
							}
						} else {
							// Non-literal arg (e.g., len(items)) - assume preallocated.
							hasCap = true
						}
					}
					return &zeroCapSliceDecl{name: name, pos: pos, hasMakeCapacity: hasCap}
				}
			}
		}
		// Function call returning a slice (e.g., strings.Split) - not our concern.
		return nil
	}
	return nil
}

// scanForAppend walks a statement list looking for append() calls assigned
// to zero-capacity slice declarations.
func scanForAppend(body *ast.BlockStmt, decls map[string]*zeroCapSliceDecl, loopLine int) []preallocWarning {
	var warnings []preallocWarning
	seen := make(map[string]bool)

	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		// Look for x = append(x, ...) pattern.
		for i, lhs := range assign.Lhs {
			if i >= len(assign.Rhs) {
				continue
			}
			ident, ok := lhs.(*ast.Ident)
			if !ok {
				continue
			}
			call, ok := assign.Rhs[i].(*ast.CallExpr)
			if !ok {
				continue
			}
			fun, ok := call.Fun.(*ast.Ident)
			if !ok || fun.Name != "append" {
				continue
			}
			// Check the first arg of append matches the LHS variable.
			if len(call.Args) > 0 {
				if argIdent, ok := call.Args[0].(*ast.Ident); ok && argIdent.Name == ident.Name {
					if decl, exists := decls[ident.Name]; exists && decl != nil && !decl.hasMakeCapacity && !seen[ident.Name] {
						seen[ident.Name] = true
						warnings = append(warnings, preallocWarning{
							varName:  ident.Name,
							loopLine: loopLine,
						})
					}
				}
			}
		}
		return true
	})

	return warnings
}
