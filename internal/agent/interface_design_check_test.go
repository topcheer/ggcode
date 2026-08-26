package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func idParseFile(t *testing.T, src string) *ast.File {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "example.go", src, 0)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return file
}

func idMakeCtx(filePath, oldContent, newContent string) CheckContext {
	ctx := CheckContext{
		FilePath:   filePath,
		OldContent: oldContent,
		NewContent: newContent,
		Lang:       LangGo,
	}
	if strings.TrimSpace(newContent) != "" {
		fset := token.NewFileSet()
		goAST, err := parser.ParseFile(fset, filePath, newContent, 0)
		if err == nil {
			ctx.GoFset = fset
			ctx.GoAST = goAST
		}
	}
	return ctx
}

func TestInterfaceDesign_NonGoFile(t *testing.T) {
	ctx := CheckContext{FilePath: "foo.py", NewContent: "x = 1", Lang: LangPython}
	if w := checkInterfaceDesign(ctx); len(w) != 0 {
		t.Errorf("expected no warnings for non-Go file, got %v", w)
	}
}

func TestInterfaceDesign_TestFile(t *testing.T) {
	ctx := CheckContext{
		FilePath: "foo_test.go", Lang: LangGo,
		GoAST: &ast.File{}, NewContent: "package x",
	}
	if w := checkInterfaceDesign(ctx); len(w) != 0 {
		t.Errorf("expected no warnings for test file, got %v", w)
	}
}

func TestInterfaceDesign_FatInterface(t *testing.T) {
	src := `package x
type FatInterface interface {
	A()
	B()
	C()
	D()
	E()
	F()
	G()
	H()
}`
	ctx := idMakeCtx("example.go", "", src)
	warnings := checkInterfaceDesign(ctx)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "FatInterface") && strings.Contains(w, "8 methods") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected fat interface warning, got: %v", warnings)
	}
}

func TestInterfaceDesign_FatInterface_DeltaAware(t *testing.T) {
	old := `package x
type FatInterface interface {
	A()  B()  C()
	D()  E()  F()
	G()  H()
}`
	src := old + `
// adding a comment doesn't change the interface`
	ctx := idMakeCtx("example.go", old, src)
	warnings := checkInterfaceDesign(ctx)
	for _, w := range warnings {
		if strings.Contains(w, "FatInterface") {
			t.Errorf("expected delta-aware suppression, got: %v", warnings)
		}
	}
}

func TestInterfaceDesign_NormalInterfaceNotFlagged(t *testing.T) {
	src := `package x
type Reader interface {
	Read(p []byte) (int, error)
}`
	ctx := idMakeCtx("example.go", "", src)
	warnings := checkInterfaceDesign(ctx)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for small idiomatic interface, got: %v", warnings)
	}
}

func TestInterfaceDesign_GenericMethodName(t *testing.T) {
	src := `package x
type Handler interface {
	Do()
}`
	ctx := idMakeCtx("example.go", "", src)
	warnings := checkInterfaceDesign(ctx)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Handler") && strings.Contains(w, "generic") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected generic method name warning, got: %v", warnings)
	}
}

func TestInterfaceDesign_NonGenericSingleMethodOK(t *testing.T) {
	src := `package x
type Handler interface {
	HandleRequest(req string) error
}`
	ctx := idMakeCtx("example.go", "", src)
	for _, w := range checkInterfaceDesign(ctx) {
		if strings.Contains(w, "generic") {
			t.Errorf("unexpected generic method warning for non-generic name: %v", w)
		}
	}
}

func TestInterfaceDesign_ReturningAny(t *testing.T) {
	src := `package x
func GetData() any { return nil }`
	ctx := idMakeCtx("example.go", "", src)
	warnings := checkInterfaceDesign(ctx)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "GetData") && strings.Contains(w, "any") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected returning-any warning, got: %v", warnings)
	}
}

func TestInterfaceDesign_ReturningInterfaceEmpty(t *testing.T) {
	src := `package x
func GetData() interface{} { return nil }`
	ctx := idMakeCtx("example.go", "", src)
	warnings := checkInterfaceDesign(ctx)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "interface{}") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected interface{} return warning, got: %v", warnings)
	}
}

func TestInterfaceDesign_InterfaceUsesAny(t *testing.T) {
	src := `package x
type Storage interface {
	Get(key string) any
}`
	ctx := idMakeCtx("example.go", "", src)
	warnings := checkInterfaceDesign(ctx)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Storage") && strings.Contains(w, "any") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected interface uses-any warning, got: %v", warnings)
	}
}

func TestInterfaceDesign_ExportedWithUnexportedMethod(t *testing.T) {
	src := `package x
type Manager interface {
	PublicOp()
	privateOp()
}`
	ctx := idMakeCtx("example.go", "", src)
	warnings := checkInterfaceDesign(ctx)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Manager") && strings.Contains(w, "unexported") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected unexported-method warning, got: %v", warnings)
	}
}

func TestInterfaceDesign_UnexportedInterfaceWithUnexportedMethodOK(t *testing.T) {
	src := `package x
type manager interface {
	PublicOp()
	privateOp()
}`
	ctx := idMakeCtx("example.go", "", src)
	for _, w := range checkInterfaceDesign(ctx) {
		if strings.Contains(w, "unexported") {
			t.Errorf("should not flag unexported interface: %v", w)
		}
	}
}

func TestInterfaceDesign_SingleImpl(t *testing.T) {
	src := `package x
type Worker interface {
	Process() error
	Shutdown()
}
type myWorker struct{}
func (myWorker) Process() error { return nil }
func (myWorker) Shutdown()      {}`
	ctx := idMakeCtx("example.go", "", src)
	warnings := checkInterfaceDesign(ctx)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "Worker") && strings.Contains(w, "1 implementation") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected single-impl warning, got: %v", warnings)
	}
}

func TestInterfaceDesign_MultipleImplOK(t *testing.T) {
	src := `package x
type Worker interface {
	Process() error
	Shutdown()
}
type workerA struct{}
func (workerA) Process() error { return nil }
func (workerA) Shutdown()      {}
type workerB struct{}
func (workerB) Process() error { return nil }
func (workerB) Shutdown()      {}`
	ctx := idMakeCtx("example.go", "", src)
	for _, w := range checkInterfaceDesign(ctx) {
		if strings.Contains(w, "1 implementation") {
			t.Errorf("should not flag multi-impl: %v", w)
		}
	}
}

func TestInterfaceDesign_SingleImplBelowThresholdOK(t *testing.T) {
	// Single-method interfaces are excluded from single-impl check (common for mocking).
	src := `package x
type Reader interface {
	Read() error
}
type myReader struct{}
func (myReader) Read() error { return nil }`
	ctx := idMakeCtx("example.go", "", src)
	for _, w := range checkInterfaceDesign(ctx) {
		if strings.Contains(w, "1 implementation") {
			t.Errorf("should not flag single-method interface: %v", w)
		}
	}
}

func TestInterfaceDesign_SingleImplDeltaAware(t *testing.T) {
	// If the interface already existed in old content, should not re-report.
	old := `package x
type Worker interface {
	Process() error
	Shutdown()
}`
	src := old + `
type myWorker struct{}
func (myWorker) Process() error { return nil }
func (myWorker) Shutdown()      {}`
	ctx := idMakeCtx("example.go", old, src)
	for _, w := range checkInterfaceDesign(ctx) {
		if strings.Contains(w, "1 implementation") {
			t.Errorf("expected delta-aware suppression for existing interface, got: %v", w)
		}
	}
}

func TestInterfaceDesign_MaxWarningsCap(t *testing.T) {
	var methods []string
	for i := 0; i < 15; i++ {
		methods = append(methods, string(rune('A'+i))+"()")
	}
	src := "package x\n" +
		"type A1 interface { " + strings.Join(methods[:9], " ") + " }\n" +
		"type A2 interface { " + strings.Join(methods[:9], " ") + " }\n" +
		"type A3 interface { " + strings.Join(methods[:9], " ") + " }\n" +
		"type A4 interface { " + strings.Join(methods[:9], " ") + " }\n" +
		"type A5 interface { " + strings.Join(methods[:9], " ") + " }\n" +
		"type A6 interface { " + strings.Join(methods[:9], " ") + " }\n"
	ctx := idMakeCtx("example.go", "", src)
	warnings := checkInterfaceDesign(ctx)
	if len(warnings) > idMaxWarnings {
		t.Errorf("expected at most %d warnings, got %d", idMaxWarnings, len(warnings))
	}
}

func TestIdCountMethods(t *testing.T) {
	src := `package x
type T interface {
	A()
	B()
	C()
	io.Reader
}`
	file := idParseFile(t, src)
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != "T" {
				continue
			}
			it, ok := ts.Type.(*ast.InterfaceType)
			if !ok {
				t.Fatal("expected InterfaceType")
			}
			if got := idCountMethods(it); got != 4 {
				t.Errorf("expected 4 methods (3 named + 1 embedded), got %d", got)
			}
		}
	}
}

func TestIdIsAnyType(t *testing.T) {
	src := `package x
var _ any
var _ interface{}`
	file := idParseFile(t, src)
	count := 0
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if idIsAnyType(vs.Type) {
				count++
			}
		}
	}
	if count != 2 {
		t.Errorf("expected 2 any types, got %d", count)
	}
}

func TestIdNamingIssue(t *testing.T) {
	if idNamingIssue("Reader", "Read") != "" {
		t.Error("Reader/Read should be idiomatic")
	}
	if idNamingIssue("Closer", "Close") != "" {
		t.Error("Closer/Close should be idiomatic")
	}
	if idNamingIssue("Handler", "Do") == "" {
		t.Error("Handler/Do should be flagged")
	}
	if idNamingIssue("Handler", "HandleRequest") != "" {
		t.Error("Handler/HandleRequest should be OK (non-generic, descriptive)")
	}
}

func TestIdImplementsAll(t *testing.T) {
	ifaceMethods := map[string]bool{"A": true, "B": true}
	typeMethods := map[string]bool{"A": true, "B": true, "C": true}
	if !idImplementsAll(ifaceMethods, typeMethods) {
		t.Error("should implement all")
	}
	partial := map[string]bool{"A": true}
	if idImplementsAll(ifaceMethods, partial) {
		t.Error("should NOT implement all (missing B)")
	}
}

func TestIdSliceEqual(t *testing.T) {
	if !idSliceEqual([]string{"a", "b"}, []string{"a", "b"}) {
		t.Error("should be equal")
	}
	if idSliceEqual([]string{"a"}, []string{"a", "b"}) {
		t.Error("different lengths should not be equal")
	}
	if idSliceEqual([]string{"a", "b"}, []string{"a", "c"}) {
		t.Error("different elements should not be equal")
	}
}

func TestIdFuncDisplayName(t *testing.T) {
	src := `package x
type Foo struct{}
func (f Foo) Bar() {}
func Plain() {}`
	file := idParseFile(t, src)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		name := idFuncDisplayName(fn)
		switch fn.Name.Name {
		case "Bar":
			if name != "Foo.Bar" {
				t.Errorf("expected Foo.Bar, got %s", name)
			}
		case "Plain":
			if name != "Plain" {
				t.Errorf("expected Plain, got %s", name)
			}
		}
	}
}

func TestInterfaceDesign_EmptyContent(t *testing.T) {
	ctx := CheckContext{FilePath: "example.go", Lang: LangGo}
	if w := checkInterfaceDesign(ctx); len(w) != 0 {
		t.Errorf("expected no warnings for empty content, got %v", w)
	}
}

func TestInterfaceDesign_NilGoAST(t *testing.T) {
	ctx := CheckContext{FilePath: "example.go", Lang: LangGo, NewContent: "invalid go code !!"}
	// GoAST will be nil because parse fails
	if w := checkInterfaceDesign(ctx); len(w) != 0 {
		t.Errorf("expected no warnings when AST is nil, got %v", w)
	}
}

// #1066: Test that generic receivers are handled correctly.
// A single-implementation interface with a generic receiver method
// should NOT be flagged as a false positive.
func TestInterfaceDesign_GenericReceiver(t *testing.T) {
	oldCode := `package main

type MyType[T any] struct{}

func (m *MyType[T]) Method() {}

type MyInterface interface {
	Method()
}

type MyImpl struct{}

func (m *MyImpl) Method() {}
`

	newCode := `package main

type MyType[T any] struct{}

func (m *MyType[T]) Method() {}

type MyInterface interface {
	Method()
}

type MyImpl struct{}

func (m *MyImpl) Method() {}

type AnotherImpl[T any] struct{}

func (a *AnotherImpl[T]) Method() {}
`

	// Adding a new implementation (AnotherImpl) to a single-impl interface
	// should NOT trigger a false positive when the receiver is generic
	ctx := idMakeCtx("example.go", oldCode, newCode)
	warnings := checkInterfaceDesign(ctx)

	// Should not report "interface has only 1 implementation" for MyInterface
	// because AnotherImpl has a generic receiver and idRecvTypeName should
	// preserve the "AnotherImpl[T]" form
	hasSingleImplWarning := false
	for _, w := range warnings {
		if strings.Contains(w, "only 1 implementation") {
			hasSingleImplWarning = true
			break
		}
	}
	if hasSingleImplWarning {
		t.Errorf("expected no single-implementation warning when generic receiver is present (#1066), got: %v", warnings)
	}
}

// #1066: Test that non-generic single-impl interface is still detected.
// This verifies the fix doesn't break the original functionality.
func TestInterfaceDesign_NonGenericSingleImpl(t *testing.T) {
	oldCode := `package main

type MyInterface interface {
	Method()
}

type MyImpl struct{}

func (m *MyImpl) Method() {}
`

	newCode := `package main

type MyInterface interface {
	Method()
}

type MyImpl struct{}

func (m *MyImpl) Method() {}

type NewImpl struct{}

func (n *NewImpl) Method() {}
`

	// Adding a new non-generic implementation should still work
	ctx := idMakeCtx("example.go", oldCode, newCode)
	warnings := checkInterfaceDesign(ctx)

	// MyInterface now has 2 implementations, should NOT warn
	hasSingleImplWarning := false
	for _, w := range warnings {
		if strings.Contains(w, "only 1 implementation") {
			hasSingleImplWarning = true
			break
		}
	}
	if hasSingleImplWarning {
		t.Errorf("expected no warning when interface has 2 implementations, got: %v", warnings)
	}
}
