package agent

import (
	"testing"
)

func TestExtractImpactSymbols(t *testing.T) {
	src := `package foo

import "fmt"

type MyType struct {
	Field string
}

func MyFunc() {}
func (m *MyType) Method1() {}
func (m MyType) Method2() {}
func privateFunc() {}

const MyConst = 42
var MyVar = "hello"
const privateConst = 1
`

	syms := extractImpactSymbols(src, "test.go")
	if len(syms) == 0 {
		t.Fatal("expected exported symbols, got none")
	}

	names := make(map[string]string)
	for _, s := range syms {
		names[s.name] = s.category
	}

	checks := map[string]string{
		"MyFunc":            "func",
		"MyType":            "type",
		"MyConst":           "const",
		"MyVar":             "var",
		"(*MyType).Method1": "method",
		"(*MyType).Method2": "method",
	}
	for name, cat := range checks {
		got, ok := names[name]
		if !ok {
			t.Errorf("expected symbol %q not found", name)
		} else if got != cat {
			t.Errorf("symbol %q: expected category %q, got %q", name, cat, got)
		}
	}

	for name := range names {
		if name == "privateFunc" || name == "privateConst" {
			t.Errorf("private symbol %q should not be exported", name)
		}
	}
}

func TestExtractImpactRemovedSymbols(t *testing.T) {
	oldSrc := `package foo
func FuncA() {}
func FuncB() {}
func FuncC() {}
type TypeX struct{}
const ConstY = 1
`
	newSrc := `package foo
func FuncA() {}
func FuncC() {}
const ConstY = 1
`
	removed := extractImpactRemovedSymbols(oldSrc, newSrc, "test.go")
	if len(removed) != 2 {
		t.Fatalf("expected 2 removed symbols, got %d: %v", len(removed), removed)
	}

	removedSet := make(map[string]bool)
	for _, s := range removed {
		removedSet[s.name] = true
	}
	if !removedSet["FuncB"] {
		t.Error("expected FuncB to be removed")
	}
	if !removedSet["TypeX"] {
		t.Error("expected TypeX to be removed")
	}
}

func TestExtractImpactRemovedSymbols_NoneRemoved(t *testing.T) {
	oldSrc := `package foo
func FuncA() {}
type TypeX struct{}
`
	newSrc := `package foo
func FuncA() {}
type TypeX struct{}
`
	removed := extractImpactRemovedSymbols(oldSrc, newSrc, "test.go")
	if len(removed) != 0 {
		t.Fatalf("expected 0 removed symbols, got %d", len(removed))
	}
}

func TestContainsGoIdent(t *testing.T) {
	tests := []struct {
		src  string
		name string
		want bool
	}{
		{"func FooBar()", "FooBar", true},
		{"x := FooBar()", "FooBar", true},
		{"type FooBar struct{}", "FooBar", true},
		{"FooBar := 1", "FooBar", true},
		{"FooBarExtended()", "FooBar", false},
		{"myFooBar()", "FooBar", false},
		{"FooBar_x", "FooBar", false},
		{"FooBar", "FooBar", true},
		{"prefix FooBar", "FooBar", true},
	}

	for _, tt := range tests {
		got := containsGoIdent(tt.src, tt.name)
		if got != tt.want {
			t.Errorf("containsGoIdent(%q, %q) = %v, want %v", tt.src, tt.name, got, tt.want)
		}
	}
}

func TestReferencesAnyImpactSymbol(t *testing.T) {
	removed := []impactRemovedSymbol{
		{name: "FuncB", category: "func"},
		{name: "TypeX", category: "type"},
		{name: "(*MyType).Method1", category: "method"},
	}

	if !referencesAnyImpactSymbol(`x := FuncB()`, removed) {
		t.Error("expected reference to FuncB")
	}
	if !referencesAnyImpactSymbol(`var x TypeX`, removed) {
		t.Error("expected reference to TypeX")
	}
	if !referencesAnyImpactSymbol(`obj.Method1()`, removed) {
		t.Error("expected reference to Method1")
	}
	if referencesAnyImpactSymbol(`var x = OtherFunc()`, removed) {
		t.Error("did not expect any reference")
	}
}

func TestImpactReceiverTypeName(t *testing.T) {
	// Test pointer receiver.
	src := "package p\nfunc (r *MyType) Foo() {}\n"
	syms := extractImpactSymbols(src, "test.go")
	found := false
	for _, s := range syms {
		if s.category == "method" && s.name == "(*MyType).Foo" {
			found = true
		}
	}
	if !found {
		t.Error("expected method (*MyType).Foo")
	}

	// Test value receiver.
	src2 := "package p\nfunc (r MyType) Bar() {}\n"
	syms2 := extractImpactSymbols(src2, "test.go")
	found2 := false
	for _, s := range syms2 {
		if s.category == "method" && s.name == "(*MyType).Bar" {
			found2 = true
		}
	}
	if !found2 {
		t.Error("expected method (*MyType).Bar for value receiver")
	}
}

func TestCrossFileImpactStateReset(t *testing.T) {
	s := newCrossFileImpactState()
	s.fired = true
	s.reset()
	if s.fired {
		t.Error("expected fired=false after reset")
	}
}

func TestCheckCrossFileImpact_NoFiles(t *testing.T) {
	a := &Agent{crossFileImpact: newCrossFileImpactState()}
	stats := &RunStats{}
	msg := a.checkCrossFileImpact(stats)
	if msg != "" {
		t.Errorf("expected empty message with no edited files, got: %s", msg)
	}
}

func TestCheckCrossFileImpact_FiresOnce(t *testing.T) {
	a := &Agent{crossFileImpact: newCrossFileImpactState()}
	stats := &RunStats{}
	// First call - fires (returns empty since no Go files, but marks fired).
	_ = a.checkCrossFileImpact(stats)
	// Second call - should short-circuit.
	msg := a.checkCrossFileImpact(stats)
	if msg != "" {
		t.Error("expected empty on second call (already fired)")
	}
	if !a.crossFileImpact.fired {
		t.Error("expected fired=true after checkCrossFileImpact")
	}
}
