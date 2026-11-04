package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// Issue #5010: impactRefVisitor's CompositeLit case skipped node.Type, so a
// sibling-file reference whose ONLY hit is at composite-literal type position
// (e.g. `myStruct{Field: 1}`, `[]myStruct{...}`) was silently missed and the
// file never entered the affected set.

// The test drives the production visitor (impactRefVisitor) over parsed
// sibling-file content and asserts the type-position Ident is now found.
func findImpactRef(t *testing.T, src string, names map[string]bool) bool {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "sib.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	v := &impactRefVisitor{names: names, methodOwners: map[string]map[string]bool{}}
	ast.Walk(v, f)
	return v.found
}

// Regression matrix from the issue body: each form previously missed must now
// report found=true; the field-name FP guard must keep reporting found=false.
func TestIssue5010CompositeLitTypePosition(t *testing.T) {
	names := map[string]bool{"myStruct": true}

	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"struct literal type", "package p\n\nvar x = myStruct{Field: 1}\n", true},
		{"return literal", "package p\n\nfunc f() myStruct { return myStruct{} }\n", true},
		{"slice literal type", "package p\n\nvar xs = []myStruct{{}}\n", true},
		{"map value literal type", "package p\n\nvar m = map[string]myStruct{}\n", true},
		{"value spec unchanged", "package p\n\nvar x myStruct\n", true},
		{"key-only hit must stay ignored", "package p\n\nvar x = other{myStruct: 1}\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := findImpactRef(t, tc.src, names); got != tc.want {
				t.Fatalf("findImpactRef = %v, want %v", got, tc.want)
			}
		})
	}
}
