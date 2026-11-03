package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestRangeNilGuardRequiresTermination(t *testing.T) {
	src := `package p
import "log"
func f(items *[]int) {
	if items == nil {
		log.Printf("items is nil")
	}
	for range *items {
	}
}`
	warnings := checkRangeNilPtr("x.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("non-terminating guard before range *items must still warn")
	}
}

func TestRangeNilGuardTerminatingNoWarn(t *testing.T) {
	src := `package p
func f(items *[]int) {
	if items == nil {
		return
	}
	for range *items {
	}
}`
	warnings := checkRangeNilPtr("x.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("terminating guard should suppress warning, got %v", warnings)
	}
}

var _ = func() bool {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", "package p", 0)
	if err != nil || len(f.Decls) == 0 {
		return false
	}
	fd, ok := f.Decls[0].(*ast.FuncDecl)
	return ok && fd.Body != nil
}
