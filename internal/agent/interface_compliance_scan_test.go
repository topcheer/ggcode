package agent

import (
	"os"
	"path/filepath"
	"testing"

	"go/ast"
	"go/parser"
	"go/token"
)

// parseTestFile helper parses src as a single Go file for helper-level tests.
func parseTestFile(t *testing.T, src string) *ast.File {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, 0)
	if err != nil {
		t.Fatalf("parse src: %v", err)
	}
	return f
}

func TestStructEmbeddedFields(t *testing.T) {
	f := parseTestFile(t, `package p
type Base struct{}
type Wrapper struct {
	Base
	*BasePtr
	Name string
	N    int
	unexportedEmbedded
}
`)
	decls := f.Decls
	if len(decls) == 0 {
		t.Fatal("no decls parsed")
	}
	// Find Wrapper struct type.
	var wrapper *ast.StructType
	for _, d := range decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, s := range gd.Specs {
			ts, ok := s.(*ast.TypeSpec)
			if ok && ts.Name != nil && ts.Name.Name == "Wrapper" {
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					t.Fatal("Wrapper is not a struct type")
				}
				wrapper = st
			}
		}
	}
	if wrapper == nil {
		t.Fatal("Wrapper type not found")
	}
	got := structEmbeddedFields(wrapper)
	want := []string{"Base", "BasePtr", "unexportedEmbedded"}
	if len(got) != len(want) {
		t.Fatalf("structEmbeddedFields = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("structEmbeddedFields[%d] = %q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}
}

func TestStructEmbeddedFieldsNilFields(t *testing.T) {
	if got := structEmbeddedFields(&ast.StructType{}); got != nil {
		t.Fatalf("expected nil for Fields==nil, got %v", got)
	}
}

func TestCollectStructTypeDeclsLaterFileOverwrites(t *testing.T) {
	typeDecls := make(map[string][]string)
	f1 := parseTestFile(t, `package p
type A struct{ Old }
`)
	collectStructTypeDecls(f1, typeDecls)
	if got := typeDecls["A"]; len(got) != 1 || got[0] != "Old" {
		t.Fatalf("after first file: %v", got)
	}
	f2 := parseTestFile(t, `package p
type A struct{ New1; New2 }
`)
	collectStructTypeDecls(f2, typeDecls)
	got := typeDecls["A"]
	if len(got) != 2 || got[0] != "New1" || got[1] != "New2" {
		t.Fatalf("later file should overwrite same-named type: %v", got)
	}
}

func TestParsePackageFilesFiltersAndCollects(t *testing.T) {
	dir := t.TempDir()
	writeFile := func(name, src string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	writeFile("edited.go", "package p\n")      // excluded via excludeFile
	writeFile("edited_test.go", "package p\n") // test file, excluded
	writeFile("notgo.txt", "not go")           // non-Go, excluded
	writeFile("a.go", "package p\ntype A struct{ Base }\n")
	writeFile("broken.go", "this is not go source")

	typeDecls := make(map[string][]string)
	files := parsePackageFiles(dir, filepath.Join(dir, "edited.go"), typeDecls)
	if len(files) != 1 {
		t.Fatalf("expected 1 parsed file (a.go), got %d", len(files))
	}
	emb := typeDecls["A"]
	if len(emb) != 1 || emb[0] != "Base" {
		t.Fatalf("typeDecls[A] = %v, want [Base]", emb)
	}
	if _, ok := typeDecls["Old"]; ok {
		t.Fatal("edited file types must not be collected")
	}
}

func TestParsePackageFilesMissingDir(t *testing.T) {
	typeDecls := make(map[string][]string)
	if files := parsePackageFiles(filepath.Join(t.TempDir(), "nope"), "x.go", typeDecls); files != nil {
		t.Fatalf("missing dir should return nil, got %d files", len(files))
	}
}

func TestCollectReceiverMethods(t *testing.T) {
	f1 := parseTestFile(t, `package p
func (a A) Foo() {}
func (a *A) Bar() {}
func FreeFunc() {}
`)
	f2 := parseTestFile(t, `package p
func (b B) Foo() {}
`)
	methods := collectReceiverMethods([]*ast.File{f1, f2})
	aMethods := methods["A"]
	if !aMethods["Foo"] || !aMethods["Bar"] {
		t.Fatalf("A methods = %v, want Foo+Bar", aMethods)
	}
	if len(aMethods) != 2 {
		t.Fatalf("A methods = %v", aMethods)
	}
	if methods["B"]["Foo"] != true {
		t.Fatalf("B methods = %v", methods["B"])
	}
	if _, ok := methods["FreeFunc"]; ok {
		t.Fatal("free functions must not be collected as methods")
	}
}

func TestPromoteEmbeddedMethodSetsTransitive(t *testing.T) {
	methods := map[string]map[string]bool{
		"C": {"Run": true},
		"B": {"Walk": true},
		"A": {"Own": true},
	}
	typeDecls := map[string][]string{
		"A": {"B"},
		"B": {"C"},
	}
	got := promoteEmbeddedMethodSets(methods, typeDecls)
	a := got["A"]
	for _, m := range []string{"Own", "Walk", "Run"} {
		if !a[m] {
			t.Fatalf("A should promote %q transitively; got %v", m, a)
		}
	}
	if len(a) != 3 {
		t.Fatalf("A method set = %v, want exactly 3", a)
	}
	// The own-methods input map must not be mutated by promotion.
	if len(methods["A"]) != 1 || !methods["A"]["Own"] {
		t.Fatalf("input methods[A] mutated: %v", methods["A"])
	}
}

func TestPromoteEmbeddedMethodSetsCycleSafe(t *testing.T) {
	methods := map[string]map[string]bool{
		"X": {"MX": true},
		"Y": {"MY": true},
	}
	typeDecls := map[string][]string{
		"X": {"Y"},
		"Y": {"X"}, // cycle
	}
	got := promoteEmbeddedMethodSets(methods, typeDecls)
	if !got["X"]["MY"] || !got["X"]["MX"] {
		t.Fatalf("X methods = %v", got["X"])
	}
	if !got["Y"]["MX"] || !got["Y"]["MY"] {
		t.Fatalf("Y methods = %v", got["Y"])
	}
}
