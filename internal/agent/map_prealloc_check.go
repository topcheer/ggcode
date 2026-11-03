package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

type mapPreallocWarning struct {
	varName   string
	loopLine  int
	sourceLen string
}

func checkMapPrealloc(filePath, oldContent, newContent string) []string {
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

	newPatterns := findMissingMapPrealloc(newAST, fset)
	if len(newPatterns) == 0 {
		return nil
	}

	if strings.TrimSpace(oldContent) != "" {
		oldAST, _ := parser.ParseFile(token.NewFileSet(), filePath, oldContent, 0)
		if oldAST != nil {
			oldPatterns := findMissingMapPrealloc(oldAST, token.NewFileSet())
			if len(oldPatterns) > 0 {
				oldCounts := make(map[string]int)
				for _, p := range oldPatterns {
					oldCounts[p.varName+"|"+p.sourceLen]++
				}
				matched := make(map[string]int, len(oldCounts))
				var delta []mapPreallocWarning
				for _, p := range newPatterns {
					key := p.varName + "|" + p.sourceLen
					if matched[key] < oldCounts[key] {
						matched[key]++
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
			warnings = append(warnings, fmt.Sprintf("...and %d more map preallocation warning(s)", len(newPatterns)-3))
			break
		}
		warnings = append(warnings, fmt.Sprintf(
			"Map preallocation: %q created with make(map[K]V) (no size hint) and populated inside a loop at %s:%d. "+
				"Each growth triggers a full rehash (O(N) per rehash, ~log(N) rehashes for N entries). "+
				"Pre-allocate with make(map[K]V, %s) to eliminate intermediate rehashes and reduce GC pressure.",
			p.varName, filepath.Base(filePath), p.loopLine, p.sourceLen))
	}
	return warnings
}

type mapDeclInfo struct {
	name string
	pos  token.Pos
}

func findMissingMapPrealloc(file *ast.File, fset *token.FileSet) []mapPreallocWarning {
	var warnings []mapPreallocWarning

	scanUnit := func(body *ast.BlockStmt, fnType *ast.FuncType) {

		binds := make(map[string]*mapDeclInfo, 4)
		collectLocalMapBinds(body, binds)
		if fnType != nil {
			excludeShadowedBinds(fnType.Params, body, binds)
		} else {
			excludeShadowedBinds(nil, body, binds)
		}

		unitWarned := make(map[string]bool)
		unsafeSources := collectUnsafeRangeSources(fnTypeParams(fnType), body)

		onLoop := func(loopBody *ast.BlockStmt, loopPos token.Pos, sourceName string, knownRange bool) {

			if sourceName != "" && unsafeSources[sourceName] {
				return
			}
			for _, m := range scanForMapWrite(loopBody, loopPos, binds) {
				if unitWarned[m] {
					continue
				}
				unitWarned[m] = true
				hint := "expectedSize"
				if knownRange && sourceName != "" {
					hint = "len(" + sourceName + ")"
				}
				warnings = append(warnings, mapPreallocWarning{
					varName:   m,
					loopLine:  fset.Position(loopPos).Line,
					sourceLen: hint,
				})
			}
		}
		scanUnitLoops(body, onLoop)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch fn := n.(type) {
		case *ast.FuncDecl:
			if fn.Body != nil {
				scanUnit(fn.Body, fn.Type)
			}
		case *ast.FuncLit:
			if fn.Body != nil {
				scanUnit(fn.Body, fn.Type)
			}
		}
		return true
	})

	return warnings
}

func collectLocalMapBinds(body *ast.BlockStmt, binds map[string]*mapDeclInfo) {
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.GenDecl:
			if node.Tok != token.VAR {
				return true
			}
			for _, spec := range node.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					if info := analyzeMapInit(name.Name, name.Pos(), vs.Values[i]); info != nil {
						binds[name.Name] = info
					}
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
				if info := analyzeMapInit(ident.Name, ident.Pos(), node.Rhs[i]); info != nil {
					binds[ident.Name] = info
				}
			}
		}
		return true
	})
}

func excludeShadowedBinds(params *ast.FieldList, body *ast.BlockStmt, binds map[string]*mapDeclInfo) {
	if len(binds) == 0 {
		return
	}
	if params != nil {
		for _, field := range params.List {
			for _, pname := range field.Names {
				delete(binds, pname.Name)
			}
		}
	}
	if body == nil {
		return
	}
	for _, stmt := range body.List {
		assign, ok := stmt.(*ast.AssignStmt)
		if !ok {
			continue
		}
		for i, rhs := range assign.Rhs {
			call, ok := rhs.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				continue
			}
			mt, ok := call.Args[0].(*ast.MapType)
			if !ok || mt == nil {
				continue
			}
			fn, ok := call.Fun.(*ast.Ident)
			if !ok || fn.Name != "make" {
				continue
			}
			if i < len(assign.Lhs) {
				if ident, ok := assign.Lhs[i].(*ast.Ident); ok {
					delete(binds, ident.Name)
				}
			}
		}
	}
}

func analyzeMapInit(name string, pos token.Pos, expr ast.Expr) *mapDeclInfo {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return nil
	}
	fun, ok := call.Fun.(*ast.Ident)
	if !ok || fun.Name != "make" {
		return nil
	}
	if len(call.Args) == 0 {
		return nil
	}

	mt, ok := call.Args[0].(*ast.MapType)
	if !ok || mt == nil {
		return nil
	}

	if len(call.Args) == 1 {
		return &mapDeclInfo{name: name, pos: pos}
	}
	return nil
}

func getRangeSourceName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		if x, ok := e.X.(*ast.Ident); ok {
			return x.Name + "." + e.Sel.Name
		}
	}
	return ""
}

var rangeUnsafeBuiltins = map[string]bool{
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"uintptr": true, "byte": true, "rune": true,
}

func collectUnsafeRangeSources(params *ast.FieldList, body *ast.BlockStmt) map[string]bool {
	unsafe := make(map[string]bool)
	markType := func(name string, typ ast.Expr) {
		if name == "" || typ == nil {
			return
		}
		switch t := typ.(type) {
		case *ast.ChanType, *ast.FuncType:
			unsafe[name] = true
		case *ast.Ident:
			if rangeUnsafeBuiltins[t.Name] {
				unsafe[name] = true
			}
		}
	}
	if params != nil {
		for _, field := range params.List {
			for _, name := range field.Names {
				markType(name.Name, field.Type)
			}
		}
	}
	if body != nil {
		ast.Inspect(body, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.FuncLit:
				return false
			case *ast.GenDecl:
				if node.Tok != token.VAR {
					return true
				}
				for _, spec := range node.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, name := range vs.Names {
						if i < len(vs.Values) {
							markUnsafeExpr(name.Name, vs.Values[i], unsafe)
						} else {
							markType(name.Name, vs.Type)
						}
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
					markUnsafeExpr(ident.Name, node.Rhs[i], unsafe)
				}
			}
			return true
		})
	}
	return unsafe
}

func markUnsafeExpr(name string, expr ast.Expr, unsafe map[string]bool) {
	if name == "" || expr == nil {
		return
	}
	switch v := expr.(type) {
	case *ast.CallExpr:
		if fn, ok := v.Fun.(*ast.Ident); ok {
			switch fn.Name {
			case "make":
				if len(v.Args) > 0 {
					if _, isChan := v.Args[0].(*ast.ChanType); isChan {
						unsafe[name] = true
					}
				}
			case "len", "cap":

				unsafe[name] = true
			}
		}
	case *ast.BasicLit:
		if v.Kind == token.INT {
			unsafe[name] = true
		}
	case *ast.FuncLit, *ast.ChanType:

		unsafe[name] = true
	}
}

type onLoopFn func(loopBody *ast.BlockStmt, loopPos token.Pos, sourceName string, knownRange bool)

func fnTypeParams(fn *ast.FuncType) *ast.FieldList {
	if fn == nil {
		return nil
	}
	return fn.Params
}

func scanUnitLoops(body *ast.BlockStmt, fn onLoopFn) {
	ast.Inspect(body, func(n ast.Node) bool {
		switch loop := n.(type) {
		case *ast.RangeStmt:
			if loop.Body == nil {
				return true
			}
			sourceName := getRangeSourceName(loop.X)
			fn(loop.Body, loop.Pos(), sourceName, true)
		case *ast.ForStmt:
			if loop.Body == nil {
				return true
			}

			fn(loop.Body, loop.Pos(), "", false)
		}
		return true
	})
}

func scanForMapWrite(body *ast.BlockStmt, loopPos token.Pos, decls map[string]*mapDeclInfo) []string {
	var result []string
	found := make(map[string]bool)

	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, lhs := range assign.Lhs {
			idx, ok := lhs.(*ast.IndexExpr)
			if !ok {
				continue
			}
			ident, ok := idx.X.(*ast.Ident)
			if !ok {
				continue
			}
			info, exists := decls[ident.Name]
			if exists && info.pos < loopPos && !found[ident.Name] {

				if hasConditionalSkip(body) {
					continue
				}
				found[ident.Name] = true
				result = append(result, ident.Name)
			}
		}
		return true
	})

	return result
}

func hasConditionalSkip(body *ast.BlockStmt) bool {
	if body == nil {
		return false
	}
	skip := false
	ast.Inspect(body, func(n ast.Node) bool {
		if skip || n == nil {
			return false
		}
		switch n.(type) {
		case *ast.IfStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
			skip = true
			return false
		case *ast.FuncLit:
			return false
		}
		return true
	})
	return skip
}
