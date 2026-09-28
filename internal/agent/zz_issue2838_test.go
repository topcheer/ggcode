package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// zz_issue2838_test.go - probe for #2838: the renamed method-value idiom
// (un := mu.Unlock; defer un()) must be exempted via its ASSIGNMENT source,
// not by variable-name substring. Conversely #2826's tightened posture must
// hold: a defer bound to a non-release method must NOT exempt a missing
// Unlock.
func parseFirstFunc2838(t *testing.T, src string) (*ast.FuncDecl, *token.FileSet) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "a.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Body != nil {
			return fn, fset
		}
	}
	t.Fatal("no func decl found")
	return nil, nil
}

func TestIssue2838RenamedMethodValueDefer(t *testing.T) {
	src := `package a
import "sync"

func renamed(mu *sync.Mutex) {
	mu.Lock()
	un := mu.Unlock
	defer un()
}
`
	fn, fset := parseFirstFunc2838(t, src)
	if fnHasIndirectRelease(fn) {
		return // exempted via assignment source - correct
	}
	instances := simulateHeldLocks(fn, fset)
	if len(instances) > 0 {
		t.Errorf("#2838 renamed method-value defer misreported as held: %+v", instances)
	}
}

func TestIssue2838NonReleaseBindingStillWarns(t *testing.T) {
	// #2826 posture: un bound to a NON-release method must not exempt, and
	// the genuinely missing Unlock must be reported.
	src := `package a
import "sync"

type T struct{ mu sync.Mutex }

func (t *T) f() {
	t.mu.Lock()
	un := t.cleanup
	defer un()
}

func (t *T) cleanup() {}
`
	fn, fset := parseFirstFunc2838(t, src)
	if fnHasIndirectRelease(fn) {
		t.Errorf("#2826 regression: defer un() bound to non-release method exempted the function")
	}
	instances := simulateHeldLocks(fn, fset)
	if len(instances) == 0 {
		t.Errorf("#2826 regression: missing Unlock not reported with unrelated defer present")
	}
}

func TestIssue2838SubstringNameStillExempt(t *testing.T) {
	src := `package a
import "sync"

func f(mu *sync.Mutex) {
	mu.Lock()
	defer unlockFn()
}
`
	fn, _ := parseFirstFunc2838(t, src)
	if !fnHasIndirectRelease(fn) {
		t.Errorf("substring-named defer (unlockFn) should stay exempt (#2826 merged behavior)")
	}
}
