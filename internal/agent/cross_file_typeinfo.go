package agent

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
)

type impactImporter struct{}

func (impactImporter) Import(path string) (*types.Package, error) {
	return nil, fmt.Errorf("imports deliberately unresolved (#2164 in-process impact scan)")
}

func impactTypeCheckGroups(fset *token.FileSet, files []*ast.File) map[string]*types.Info {
	if len(files) == 0 {
		return nil
	}
	groups := make(map[string][]*ast.File, 1)
	for _, f := range files {
		if f == nil || f.Name == nil {
			continue
		}
		groups[f.Name.Name] = append(groups[f.Name.Name], f)
	}
	if len(groups) == 0 {
		return nil
	}
	out := make(map[string]*types.Info, len(groups))
	for pkgName, fs := range groups {
		info := &types.Info{
			Types: make(map[ast.Expr]types.TypeAndValue),
			Defs:  make(map[*ast.Ident]types.Object),
			Uses:  make(map[*ast.Ident]types.Object),
		}
		conf := &types.Config{
			Importer:    impactImporter{},
			FakeImportC: true,
			Error:       func(error) {},
		}
		conf.Check(pkgName, fset, fs, info)
		out[pkgName] = info
	}
	return out
}

func impactResolveNearMiss(info *types.Info, miss impactNearMiss, methodOwners map[string]map[string]bool) bool {
	if info == nil || miss.recv == nil {
		return false
	}
	owners, ok := methodOwners[miss.sel]
	if !ok || len(owners) == 0 {
		return false
	}
	tv, ok := info.Types[miss.recv]
	if !ok || tv.Type == nil || tv.Type == types.Typ[types.Invalid] {
		return false
	}
	t := tv.Type
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil || obj.Name() == "" {
		return false
	}
	return owners[obj.Name()]
}
