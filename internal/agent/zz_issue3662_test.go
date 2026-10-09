package agent

// #3662 probe, defect 2: a METHOD's DisplayName is recvType_Method
// ("Foo_Bar"), but tests invoke the method ("x.Bar()") - the underscore
// form never appears in test code, so the #3620 reference fallback was
// dead code for methods. The bare method name must also be scanned.
// (Defect 1 - sibling-absence setting anyParseFailed - was already
// restructured away on main by the #3649 rework; verified absent.)

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIssue3662_MethodReferenceFallback(t *testing.T) {
	dir := t.TempDir()
	// Source: foo.go with an exported method on an exported type.
	src := `package agent

type Foo struct{}

func (f *Foo) Process() int { return 1 }
`
	if err := os.WriteFile(filepath.Join(dir, "foo.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	// No foo_test.go sibling; zz_test.go invokes the METHOD directly.
	tst := `package agent

import "testing"

func TestProcessFlow(t *testing.T) {
	f := &Foo{}
	if f.Process() != 1 {
		t.Fatal("bad")
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "zz_issue3662_test.go"), []byte(tst), 0o644); err != nil {
		t.Fatal(err)
	}
	got := untestedExportedFuncs(dir, "foo.go")
	if len(got) != 0 {
		t.Fatalf("method invoked by a directory test still reported untested: %v", got)
	}
	// Control: a method NOT referenced anywhere stays untested.
	tst2 := `package agent

import "testing"

func TestOther(t *testing.T) { _ = 1 }
`
	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir2, "foo.go"), []byte(src), 0o644)
	os.WriteFile(filepath.Join(dir2, "zz_other_test.go"), []byte(tst2), 0o644)
	if got := untestedExportedFuncs(dir2, "foo.go"); len(got) == 0 {
		t.Fatal("unreferenced method must stay untested (fallback too broad)")
	}
}
