package agent

// #3758 probes: two false-positive shapes in the value-receiver mutation
// detector. (A) The "return the new value" idiom (time.Time.Add et al.)
// mutates the copy on purpose and hands it back - warning on it (and
// advising a pointer receiver) damages correct immutable APIs. (B) A `:=`
// shadow of the receiver name makes later mutations of that name hit the
// shadow, not the receiver.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func vrmIssues3758(t *testing.T, src string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "probe.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var issues []string
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok {
			vrmCheckFunc(fn, fset, "probe.go", &issues, nil)
		}
	}
	return issues
}

func TestIssue3758_ReturnNewValueIdiomExempt(t *testing.T) {
	src := `package p
type Counter struct{ count int }
func (c Counter) Add(n int) Counter { c.count += n; return c }
func (c Counter) Bump() (Counter, int) { c.count++; return c, c.count }
`
	if issues := vrmIssues3758(t, src); len(issues) != 0 {
		t.Fatalf("return-new-value idiom must be exempt, got: %v", issues)
	}
}

func TestIssue3758_ShadowedReceiverNameNotFlagged(t *testing.T) {
	src := `package p
type T struct{ field int }
func (t T) Method(x int) int {
	t := T{field: x} // shadows the receiver name
	t.field = 5
	return t.field
}
`
	if issues := vrmIssues3758(t, src); len(issues) != 0 {
		t.Fatalf("shadowed-name mutation must not flag the receiver, got: %v", issues)
	}
}

func TestIssue3758_GenuineLostMutationStillFlagged(t *testing.T) {
	src := `package p
type T struct{ field int }
func (t T) Set(v int) {
	t.field = v // genuinely lost - no return, no shadow
}
`
	if issues := vrmIssues3758(t, src); len(issues) == 0 {
		t.Fatal("a genuine lost mutation must still be flagged")
	}
}
