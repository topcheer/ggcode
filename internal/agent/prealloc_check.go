package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

type preallocWarning struct {
	pos      token.Pos
	varName  string
	loopLine int
}

func checkMissingPrealloc(filePath, oldContent, newContent string) []string {
	if !strings.HasSuffix(filePath, ".go") {
		return nil
	}
	if strings.HasSuffix(filePath, "_test.go") {
		return nil
	}
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	newAST, fset, err := parseGoSource(filePath, newContent, 0)
	if err != nil {
		return nil
	}

	newPatterns := findMissingPrealloc(newAST, fset)
	if len(newPatterns) == 0 {
		return nil
	}

	if strings.TrimSpace(oldContent) != "" {
		oldAST, _ := parser.ParseFile(token.NewFileSet(), filePath, oldContent, 0)
		if oldAST != nil {
			oldPatterns := findMissingPrealloc(oldAST, token.NewFileSet())
			if len(oldPatterns) > 0 {
				oldCounts := make(map[string]int)
				for _, p := range oldPatterns {
					oldCounts[p.varName]++
				}
				matched := make(map[string]int, len(oldCounts))
				var delta []preallocWarning
				for _, p := range newPatterns {
					if matched[p.varName] < oldCounts[p.varName] {
						matched[p.varName]++
						continue
					}
					delta = append(delta, p)
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
		if i >= 3 {
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

type zeroCapSliceDecl struct {
	name            string
	pos             token.Pos
	hasMakeCapacity bool
}

func findMissingPrealloc(file *ast.File, fset *token.FileSet) []preallocWarning {
	pkgDecls := make(map[string]*zeroCapSliceDecl)
	for _, vs := range packageVarSpecs(file) {
		addValueSpecSliceDecls(vs, pkgDecls)
	}

	var warnings []preallocWarning
	seenLoopWarn := make(map[string]bool)

	scanUnit := func(body *ast.BlockStmt) {

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

			if loopHasEarlyExit(loopBody) {
				return
			}
			for _, w := range scanForAppend(loopBody, decls, loopLine) {
				key := fmt.Sprintf("%d:%s", loopLine, w.varName)
				if unitWarned[w.varName] || seenLoopWarn[key] {
					continue
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
				scanUnit(fn.Body)
			}
		}
		return true
	})

	return warnings
}

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

func addValueSpecSliceDecls(vs *ast.ValueSpec, decls map[string]*zeroCapSliceDecl) {
	for i, name := range vs.Names {
		if vs.Type != nil {

			if _, isSlice := vs.Type.(*ast.ArrayType); isSlice {
				if i < len(vs.Values) {
					if d := analyzeInitExpr(name.Name, name.Pos(), vs.Values[i]); d != nil {
						recordDeclRespectingConflicts(decls, d)
					}
				} else {
					recordDeclRespectingConflicts(decls, &zeroCapSliceDecl{
						name: name.Name, pos: name.Pos(), hasMakeCapacity: false,
					})
				}
			}
		} else if i < len(vs.Values) {
			if d := analyzeInitExpr(name.Name, name.Pos(), vs.Values[i]); d != nil {
				recordDeclRespectingConflicts(decls, d)
			}
		}
	}
}

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
			for i, lhs := range node.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok || i >= len(node.Rhs) {
					continue
				}
				d := analyzeInitExpr(ident.Name, ident.Pos(), node.Rhs[i])
				if node.Tok == token.DEFINE {
					if d != nil {
						recordDeclRespectingConflicts(decls, d)
					}
					continue
				}

				if d != nil && d.hasMakeCapacity {
					if prev, ok := decls[ident.Name]; ok && !prev.hasMakeCapacity {
						upgraded := *prev
						upgraded.hasMakeCapacity = true
						decls[ident.Name] = &upgraded
					}
				}
			}
		}
		return true
	})
}

func recordDeclRespectingConflicts(decls map[string]*zeroCapSliceDecl, d *zeroCapSliceDecl) {
	if prev, ok := decls[d.name]; ok && prev.hasMakeCapacity != d.hasMakeCapacity {
		merged := *d
		merged.hasMakeCapacity = true
		decls[d.name] = &merged
		return
	}
	decls[d.name] = d
}

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

func loopHasEarlyExit(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		if bs, ok := n.(*ast.BranchStmt); ok {
			switch bs.Tok {
			case token.BREAK, token.CONTINUE, token.GOTO:
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func analyzeInitExpr(name string, pos token.Pos, expr ast.Expr) *zeroCapSliceDecl {

	if cl, ok := expr.(*ast.CompositeLit); ok {
		if _, isSlice := cl.Type.(*ast.ArrayType); isSlice {
			return &zeroCapSliceDecl{name: name, pos: pos, hasMakeCapacity: false}
		}
		return nil
	}

	if call, ok := expr.(*ast.CallExpr); ok {
		if fun, ok := call.Fun.(*ast.Ident); ok && fun.Name == "make" {

			if len(call.Args) > 0 {
				if _, isSlice := call.Args[0].(*ast.ArrayType); isSlice {

					hasCap := len(call.Args) >= 3
					if !hasCap && len(call.Args) == 2 {

						if lit, ok := call.Args[1].(*ast.BasicLit); ok {
							if lit.Value != "0" {
								hasCap = true
							}
						} else {

							hasCap = true
						}
					}
					return &zeroCapSliceDecl{name: name, pos: pos, hasMakeCapacity: hasCap}
				}
			}
		}

		return nil
	}
	return nil
}

func scanForAppend(body *ast.BlockStmt, decls map[string]*zeroCapSliceDecl, loopLine int) []preallocWarning {
	var warnings []preallocWarning
	seen := make(map[string]bool)

	for _, stmt := range body.List {
		assign, ok := stmt.(*ast.AssignStmt)
		if !ok {
			continue
		}

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
	}

	return warnings
}
