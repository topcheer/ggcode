package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func parseGoTest(t *testing.T, src string) *ast.File {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "test.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return file
}

func TestCheckMissingExportedDocs_NewExportedFuncNoDoc(t *testing.T) {
	old := `package foo
`
	newSrc := `package foo

func ExportedFunc() {}
`
	warnings := checkMissingExportedDocsAST("foo.go", old, parseGoTest(t, newSrc))
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], `ExportedFunc`) {
		t.Errorf("warning should mention ExportedFunc: %s", warnings[0])
	}
	if !strings.Contains(warnings[0], "function") {
		t.Errorf("warning should mention kind 'function': %s", warnings[0])
	}
}

func TestCheckMissingExportedDocs_NewExportedTypeNoDoc(t *testing.T) {
	old := `package foo
`
	newSrc := `package foo

type Config struct {
	Field string
}
`
	warnings := checkMissingExportedDocsAST("foo.go", old, parseGoTest(t, newSrc))
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for type, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "type") {
		t.Errorf("warning should mention kind 'type': %s", warnings[0])
	}
}

func TestCheckMissingExportedDocs_WithDocNoWarning(t *testing.T) {
	newSrc := `package foo

// ExportedFunc does something useful.
func ExportedFunc() {}
`
	warnings := checkMissingExportedDocsAST("foo.go", "", parseGoTest(t, newSrc))
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings when doc present, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckMissingExportedDocs_PreExistingNoWarning(t *testing.T) {
	// Exported func was already missing a doc before the edit - should not warn.
	old := `package foo

func ExportedFunc() {}
`
	newSrc := `package foo

func ExportedFunc() {}

func AnotherFunc() {}
`
	warnings := checkMissingExportedDocsAST("foo.go", old, parseGoTest(t, newSrc))
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning (only AnotherFunc), got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "AnotherFunc") {
		t.Errorf("should only warn about new func: %s", warnings[0])
	}
}

func TestCheckMissingExportedDocs_UnexportedNoWarning(t *testing.T) {
	newSrc := `package foo

func internalFunc() {}
type myType struct{}
`
	warnings := checkMissingExportedDocsAST("foo.go", "", parseGoTest(t, newSrc))
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for unexported identifiers, got %d", len(warnings))
	}
}

func TestCheckMissingExportedDocs_TestFileSkipped(t *testing.T) {
	newSrc := `package foo

func ExportedHelper() {}
`
	warnings := checkMissingExportedDocsAST("foo_test.go", "", parseGoTest(t, newSrc))
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings in test file, got %d", len(warnings))
	}
}

func TestCheckMissingExportedDocs_MainPackageSkipped(t *testing.T) {
	newSrc := `package main

func ExportedThing() {}
`
	warnings := checkMissingExportedDocsAST("main.go", "", parseGoTest(t, newSrc))
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings in main package, got %d", len(warnings))
	}
}

func TestCheckMissingExportedDocs_ConstGroupMultiSpec(t *testing.T) {
	// In a multi-spec const group, each exported const needs its own doc.
	newSrc := `package foo

const (
	A = 1
	B = 2
)
`
	warnings := checkMissingExportedDocsAST("foo.go", "", parseGoTest(t, newSrc))
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings for multi-spec const group, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckMissingExportedDocs_SingleSpecDocOnGenDecl(t *testing.T) {
	// Single-spec declaration: doc on the GenDecl is sufficient.
	newSrc := `package foo

// Config holds settings.
type Config struct{}
`
	warnings := checkMissingExportedDocsAST("foo.go", "", parseGoTest(t, newSrc))
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings when doc on GenDecl, got %d", len(warnings))
	}
}

func TestCheckMissingExportedDocs_CapAt3(t *testing.T) {
	newSrc := `package foo

func Aaa() {}
func Bbb() {}
func Ccc() {}
func Ddd() {}
func Eee() {}
`
	warnings := checkMissingExportedDocsAST("foo.go", "", parseGoTest(t, newSrc))
	if len(warnings) != 3 {
		t.Fatalf("expected warnings capped at 3, got %d", len(warnings))
	}
}

func TestCheckMissingExportedDocs_VarExported(t *testing.T) {
	newSrc := `package foo

var DefaultPort = 8080
`
	warnings := checkMissingExportedDocsAST("foo.go", "", parseGoTest(t, newSrc))
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for exported var, got %d", len(warnings))
	}
	if !strings.Contains(warnings[0], "variable") {
		t.Errorf("warning should mention kind 'variable': %s", warnings[0])
	}
}

func TestCheckMissingExportedDocs_MethodOnUnexportedType(t *testing.T) {
	// Exported method on unexported receiver type - not truly reachable externally.
	newSrc := `package foo

type myStruct struct{}

func (m *myStruct) ExportedMethod() {}
`
	warnings := checkMissingExportedDocsAST("foo.go", "", parseGoTest(t, newSrc))
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for method on unexported type, got %d", len(warnings))
	}
}
