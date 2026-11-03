package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

type nilMapWriteInstance struct {
	posStr   string
	mapName  string
	funcName string
}

func checkNilMapWrite(filePath, oldContent, newContent string) string {
	if filepath.Ext(filePath) != ".go" || strings.TrimSpace(newContent) == "" {
		return ""
	}

	file, fset, err := parseGoSource(filePath, newContent, parser.AllErrors)
	if err != nil {
		return ""
	}

	var instances []nilMapWriteInstance

	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}
		if fn.Body == nil {
			return true
		}

		instances = append(instances, findNilMapWritesInFunc(fset, fn)...)
		return true
	})

	if len(instances) == 0 {
		return ""
	}

	var oldInstances []nilMapWriteInstance
	if strings.TrimSpace(oldContent) != "" {
		oldFset := token.NewFileSet()
		oldFile, oldErr := parser.ParseFile(oldFset, filePath, oldContent, parser.AllErrors)
		if oldErr == nil {
			ast.Inspect(oldFile, func(n ast.Node) bool {
				fn, ok := n.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					return true
				}
				oldInstances = append(oldInstances, findNilMapWritesInFunc(oldFset, fn)...)
				return true
			})
		}
	}

	oldSet := make(map[string]bool, len(oldInstances))
	for _, oi := range oldInstances {
		oldSet[oi.funcName+"|"+oi.mapName] = true
	}

	var newInstances []nilMapWriteInstance
	for _, inst := range instances {
		if !oldSet[inst.funcName+"|"+inst.mapName] {
			newInstances = append(newInstances, inst)
		}
	}

	if len(newInstances) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("[nil-map-write] Maps written without make() initialization - will panic:\n")
	for _, inst := range newInstances {
		b.WriteString(fmt.Sprintf("  - %s: map '%s' needs `make()` before write.\n",
			inst.posStr, inst.mapName))

	}
	b.WriteString("Note: nil maps are valid for reads (return zero value) but PANIC on any write operation.")
	return b.String()
}

func findNilMapWritesInFunc(fset *token.FileSet, fn *ast.FuncDecl) []nilMapWriteInstance {
	nilMaps := collectNilRiskMaps(fn.Body)
	if len(nilMaps) == 0 {
		return nil
	}
	instances := findUninitializedMapWrites(fset, fn.Body, nilMaps)
	instances = deduplicateByMapName(instances)
	fnName := "<anonymous>"
	if fn.Name != nil {
		fnName = fn.Name.Name
	}
	for i := range instances {
		instances[i].funcName = fnName
	}
	return instances
}

func collectNilRiskMaps(body *ast.BlockStmt) map[string]bool {
	nilMaps := make(map[string]bool)
	ast.Inspect(body, func(n ast.Node) bool {
		decl, ok := n.(*ast.DeclStmt)
		if !ok {
			return true
		}
		genDecl, ok := decl.Decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.VAR {
			return true
		}
		for _, spec := range genDecl.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if vs.Type != nil {
				if _, isMap := vs.Type.(*ast.MapType); isMap && len(vs.Values) == 0 {
					for _, name := range vs.Names {
						if name.Name != "_" {
							nilMaps[name.Name] = true
						}
					}
				}
			}
		}
		return true
	})
	return nilMaps
}

func findUninitializedMapWrites(fset *token.FileSet, body *ast.BlockStmt, nilMaps map[string]bool) []nilMapWriteInstance {
	initialized := make(map[string]bool)
	var instances []nilMapWriteInstance
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range node.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok || !nilMaps[ident.Name] {

					if idx, isIdx := lhs.(*ast.IndexExpr); isIdx {
						instances = append(instances, checkNilMapWriteInstance(fset, idx, nilMaps, initialized)...)
					}
					continue
				}
				if node.Tok != token.ASSIGN || i >= len(node.Rhs) {
					continue
				}
				rhs := node.Rhs[i]
				switch {
				case isNilExpr(rhs):

					delete(initialized, ident.Name)
				case isMakeMapCall([]ast.Expr{rhs}) || isMapLiteral([]ast.Expr{rhs}):
					initialized[ident.Name] = true
				default:

					initialized[ident.Name] = true
				}
			}
			for _, lhs := range node.Lhs {
				if idx, ok := lhs.(*ast.IndexExpr); ok {
					instances = append(instances, checkNilMapWriteInstance(fset, idx, nilMaps, initialized)...)
				}
			}
		case *ast.IncDecStmt:
			if idx, ok := node.X.(*ast.IndexExpr); ok {
				instances = append(instances, checkNilMapWriteInstance(fset, idx, nilMaps, initialized)...)
			}
		}
		return true
	})
	return instances
}

func isNilExpr(e ast.Expr) bool {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name == "nil"
	case *ast.ParenExpr:
		return isNilExpr(t.X)
	}
	return false
}

func checkNilMapWriteInstance(fset *token.FileSet, idx *ast.IndexExpr, nilMaps, initialized map[string]bool) []nilMapWriteInstance {
	ident, ok := idx.X.(*ast.Ident)
	if !ok || !nilMaps[ident.Name] || initialized[ident.Name] {
		return nil
	}
	pos := fset.Position(idx.Pos())
	return []nilMapWriteInstance{{
		posStr:  fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line),
		mapName: ident.Name,
	}}
}

func deduplicateByMapName(instances []nilMapWriteInstance) []nilMapWriteInstance {
	seen := make(map[string]bool)
	var deduped []nilMapWriteInstance
	for _, inst := range instances {
		k := inst.mapName + "\x00" + inst.posStr
		if !seen[k] {
			seen[k] = true
			deduped = append(deduped, inst)
		}
	}
	return deduped
}

func isMakeMapCall(rhs []ast.Expr) bool {
	for _, expr := range rhs {
		if call, ok := expr.(*ast.CallExpr); ok {
			if fun, ok := call.Fun.(*ast.Ident); ok && fun.Name == "make" {
				if len(call.Args) > 0 {
					if _, isMap := call.Args[0].(*ast.MapType); isMap {
						return true
					}
				}
			}
		}
	}
	return false
}

func isMapLiteral(rhs []ast.Expr) bool {
	for _, expr := range rhs {
		if cl, ok := expr.(*ast.CompositeLit); ok {
			if _, isMap := cl.Type.(*ast.MapType); isMap {
				return true
			}
		}
	}
	return false
}
