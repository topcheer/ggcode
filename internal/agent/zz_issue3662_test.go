package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIssue3662_MethodReferenceFallback(t *testing.T) {
	dir := t.TempDir()

	src := `package agent

type Foo struct{}

func (f *Foo) Process() int { return 1 }
`
	if err := os.WriteFile(filepath.Join(dir, "foo.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

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
