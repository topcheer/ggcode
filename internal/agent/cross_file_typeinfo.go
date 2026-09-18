package agent

// #2164: type-informed resolution of cross-file method-call references.
//
// Governance constraints (issue #2164 owner ruling): NO x/tools dependency,
// NO packages.Load subprocess (hundreds of ms per call), NO LSP coupling,
// and NO reintroduction of the false-positive surface the #2100/#2163
// rulings closed. This file implements the remaining fourth route: a
// narrow, in-process, stdlib-only go/types pass that runs ONLY when the
// cheap syntax visitor found variable-receiver near-miss candidates.
//
// Design invariants:
//   - Zero new dependencies: go/ast + go/token + go/types only.
//   - Zero subprocesses: everything happens in-process; typical cost is
//     milliseconds for a sibling package, gated on the existing deadline.
//   - Zero false positives: a near miss is upgraded to a hit ONLY when the
//     receiver's static type is fully resolved to a named type owned by
//     the same package AND that type name is the recorded owner of the
//     removed method. Anything unresolved (broken sources, foreign
//     imports, interfaces, invalid types) stays a conservative miss -
//     exactly the pre-#2164 behavior; the compiler remains ground truth.

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
)

// impactImporter deliberately resolves NO imports. Same-package
// declarations (the dominant cross-file-impact case) need no import
// information; anything that depends on an imported package stays an
// unresolved conservative miss.
type impactImporter struct{}

func (impactImporter) Import(path string) (*types.Package, error) {
	return nil, fmt.Errorf("imports deliberately unresolved (#2164 in-process impact scan)")
}

// impactTypeCheckGroups type-checks the post-edit package sources with all
// errors swallowed, grouping files by package clause (e.g. pkg vs pkg_test).
// It returns nil infos only when the files yield nothing usable; partial
// type info from broken sources is fine - same-package declarations still
// resolve, and unresolved receivers are filtered out by callers.
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
			Error:       func(error) {}, // swallowed: partial info is the contract
		}
		conf.Check(pkgName, fset, fs, info)
		out[pkgName] = info
	}
	return out
}

// impactResolveNearMiss reports whether a near-miss selector is PROVEN by
// type info to reference a removed method: the receiver's static type must
// resolve to a named type whose name is a recorded owner of the removed
// method. Pointer deref is applied; interfaces, unions, invalid and
// unresolved types all stay misses.
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
		return false // interface/union/unresolved receiver: conservative miss
	}
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil || obj.Name() == "" {
		return false
	}
	return owners[obj.Name()]
}
