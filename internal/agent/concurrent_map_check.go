package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

type concurrentMapInstance struct {
	posStr   string
	fp       string
	mapName  string
	goCalled bool
}

func checkConcurrentMapAccess(filePath, oldContent, newContent string) string {
	if filepath.Ext(filePath) != ".go" || strings.TrimSpace(newContent) == "" {
		return ""
	}

	file, fset, err := parseGoSource(filePath, newContent, 0)
	if err != nil || file == nil {
		return ""
	}

	instances := findConcurrentMapAccess(fset, file)
	if len(instances) == 0 {
		return ""
	}

	var oldLines map[string]bool
	if strings.TrimSpace(oldContent) != "" {
		for _, iss := range findConcurrentMapAccess(token.NewFileSet(), func() *ast.File {

			f, _ := parser.ParseFile(token.NewFileSet(), filePath, oldContent, 0)
			return f
		}()) {
			if oldLines == nil {
				oldLines = make(map[string]bool)
			}
			oldLines[iss.fp] = true
		}
	}

	var b strings.Builder
	b.WriteString("[Concurrent map access detection] Potential unsynchronized concurrent map access detected.\n")
	b.WriteString("Go maps are NOT safe for concurrent use - the runtime will fatally crash with 'concurrent map read/write'.\n")
	reported := 0
	for _, inst := range instances {
		if oldLines != nil && oldLines[inst.fp] {
			continue
		}
		b.WriteString(fmt.Sprintf("  - %s: map '%s' is accessed in a function that spawns goroutines without sync (Mutex/RWMutex/sync.Map). ",
			inst.posStr, inst.mapName))
		b.WriteString("Protect with sync.RWMutex, or use sync.Map for concurrent access patterns.\n")
		reported++
	}
	if reported == 0 {

		return ""
	}
	return b.String()
}

func findConcurrentMapAccess(fset *token.FileSet, file *ast.File) []concurrentMapInstance {
	var instances []concurrentMapInstance

	pkgMaps := map[string]bool{}
	for _, topDecl := range file.Decls {
		gd, ok := topDecl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if _, isMap := vs.Type.(*ast.MapType); isMap {
				for _, name := range vs.Names {
					pkgMaps[name.Name] = true
				}
				continue
			}
			for i, val := range vs.Values {
				if isMapValuedExpr(val) && i < len(vs.Names) {
					pkgMaps[vs.Names[i].Name] = true
				}
			}
		}
	}

	for _, topDecl := range file.Decls {
		fn, ok := topDecl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		info := analyzeFuncForMapConcurrency(fn, pkgMaps)
		if len(info.unsyncMapWrites) == 0 || !info.hasGoStatement {
			continue
		}

		for mapName, pos := range info.unsyncMapWrites {
			p := fset.Position(pos)
			instances = append(instances, concurrentMapInstance{
				posStr:   fmt.Sprintf("%s:%d", filepath.Base(p.Filename), p.Line),
				fp:       fn.Name.Name + "|" + mapName,
				mapName:  mapName,
				goCalled: true,
			})
		}
	}

	return instances
}

type mapConcurrencyInfo struct {
	hasGoStatement  bool
	hasSync         bool
	unsyncMapWrites map[string]token.Pos
	mapDeclared     map[string]bool
}

func analyzeFuncForMapConcurrency(fn *ast.FuncDecl, pkgMaps map[string]bool) mapConcurrencyInfo {
	body := fn.Body
	info := mapConcurrencyInfo{
		unsyncMapWrites: make(map[string]token.Pos),
		mapDeclared:     make(map[string]bool),
	}

	for name := range pkgMaps {
		info.mapDeclared[name] = true
	}

	seed := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, fld := range fl.List {
			if _, isMap := fld.Type.(*ast.MapType); isMap {
				for _, name := range fld.Names {
					info.mapDeclared[name.Name] = true
				}
			}
		}
	}
	seed(fn.Type.Params)
	seed(fn.Recv)

	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			for _, rhs := range node.Rhs {
				if isMapValuedExpr(rhs) {
					for _, lhs := range node.Lhs {
						if id, ok := lhs.(*ast.Ident); ok {
							info.mapDeclared[id.Name] = true
						}
					}
				}
			}
		case *ast.DeclStmt:
			if d, ok := node.Decl.(*ast.GenDecl); ok && d.Tok == token.VAR {
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					if _, isMap := vs.Type.(*ast.MapType); isMap {
						for _, name := range vs.Names {
							info.mapDeclared[name.Name] = true
						}
						continue
					}

					for i, val := range vs.Values {
						if isMapValuedExpr(val) && i < len(vs.Names) {
							info.mapDeclared[vs.Names[i].Name] = true
						}
					}
				}
			}
		}
		return true
	})

	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.GoStmt:
			info.hasGoStatement = true

		case *ast.SelectorExpr:

			if ident, ok := node.X.(*ast.Ident); ok {
				if ident.Name == "sync" && node.Sel != nil {
					switch node.Sel.Name {
					case "Map", "Mutex", "RWMutex", "Locker":
						info.hasSync = true
					}
				}
			}

		case *ast.CallExpr:

			if sel, ok := node.Fun.(*ast.SelectorExpr); ok && sel.Sel != nil {
				switch sel.Sel.Name {
				case "Lock", "Unlock", "RLock", "RUnlock", "TryLock", "TryRLock":
					info.hasSync = true
				}
			}

			if ident, ok := node.Fun.(*ast.Ident); ok && ident.Name == "Lock" {
				info.hasSync = true
			}

			if ident, ok := node.Fun.(*ast.Ident); ok && ident.Name == "delete" && len(node.Args) > 0 {
				if mapName := mapVarName(node.Args[0]); mapName != "" {
					if _, exists := info.unsyncMapWrites[mapName]; !exists {
						info.unsyncMapWrites[mapName] = node.Pos()
					}
				}
			}

		case *ast.IncDecStmt:

			if idx, ok := node.X.(*ast.IndexExpr); ok {
				name := mapVarName(idx.X)
				if name != "" && (strings.Contains(name, ".") || info.mapDeclared[name]) {
					if _, exists := info.unsyncMapWrites[name]; !exists {
						info.unsyncMapWrites[name] = node.Pos()
					}
				}
			}

		case *ast.AssignStmt:

			isAssignTok := node.Tok == token.ASSIGN || node.Tok == token.DEFINE ||
				node.Tok == token.ADD_ASSIGN || node.Tok == token.SUB_ASSIGN ||
				node.Tok == token.MUL_ASSIGN || node.Tok == token.QUO_ASSIGN ||
				node.Tok == token.REM_ASSIGN || node.Tok == token.AND_ASSIGN ||
				node.Tok == token.OR_ASSIGN || node.Tok == token.XOR_ASSIGN ||
				node.Tok == token.SHL_ASSIGN || node.Tok == token.SHR_ASSIGN ||
				node.Tok == token.AND_NOT_ASSIGN
			if isAssignTok {
				for _, lhs := range node.Lhs {
					if idx, ok := lhs.(*ast.IndexExpr); ok {

						name := mapVarName(idx.X)
						isMapWrite := name != "" && (strings.Contains(name, ".") || info.mapDeclared[name])
						if isMapWrite {
							if _, exists := info.unsyncMapWrites[name]; !exists {
								info.unsyncMapWrites[name] = node.Pos()
							}
						}
					}
				}
			}
		}
		return true
	})

	if info.hasSync {
		info.unsyncMapWrites = make(map[string]token.Pos)
	}

	return info
}

func mapVarName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		if base, ok := e.X.(*ast.Ident); ok {
			return base.Name + "." + e.Sel.Name
		}
		return ""
	default:
		return ""
	}
}

func isMapValuedExpr(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.CallExpr:
		if id, ok := v.Fun.(*ast.Ident); ok && id.Name == "make" && len(v.Args) > 0 {
			if _, isMap := v.Args[0].(*ast.MapType); isMap {
				return true
			}
		}
	case *ast.CompositeLit:
		if _, isMap := v.Type.(*ast.MapType); isMap {
			return true
		}
	case *ast.ParenExpr:
		return isMapValuedExpr(v.X)
	}
	return false
}
