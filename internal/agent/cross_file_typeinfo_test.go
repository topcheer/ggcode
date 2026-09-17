package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// #2164 regression suite: variable-receiver method calls across files must
// be resolved via stdlib go/types when the syntax-only visitor records a
// near miss, with zero false positives on same-named methods of unrelated
// types and conservative misses for anything unresolved.

// buildImpactScenario parses old/new edited content, extracts removed
// symbols, and returns the pieces the typed pass consumes.
func buildImpactScenario(t *testing.T, oldSrc, newSrc, siblingSrc string) (names map[string]bool, owners map[string]map[string]bool, sibFile *ast.File, fset *token.FileSet, newFile *ast.File) {
	t.Helper()
	removed := extractImpactRemovedSymbols(oldSrc, newSrc, "a.go")
	if len(removed) == 0 {
		t.Fatalf("expected removed symbols from edited file")
	}
	names, owners = impactNameTables(removed)
	fset = token.NewFileSet()
	sib, err := parser.ParseFile(fset, "b.go", siblingSrc, 0)
	if err != nil {
		t.Fatalf("sibling parse: %v", err)
	}
	nf, err := parser.ParseFile(fset, "a.go", newSrc, 0)
	if err != nil {
		t.Fatalf("edited parse: %v", err)
	}
	return names, owners, sib, fset, nf
}

// resolveScenario runs the full two-pass pipeline (visitor + typed pass).
func resolveScenario(t *testing.T, oldSrc, newSrc, siblingSrc string) bool {
	t.Helper()
	names, owners, sib, fset, nf := buildImpactScenario(t, oldSrc, newSrc, siblingSrc)
	found, near := walkImpactRefs(sib, names, owners)
	if found {
		return true
	}
	if len(near) == 0 {
		return false
	}
	infos := impactTypeCheckGroups(fset, []*ast.File{sib, nf})
	for _, m := range near {
		if impactResolveNearMiss(infos[sib.Name.Name], m, owners) {
			return true
		}
	}
	return false
}

const impact2164Old = "package p\n\ntype Server struct{}\n\nfunc (s *Server) run() {}\n"
const impact2164New = "package p\n\ntype Server struct{}\n"

func TestImpact2164VariableReceiverHit(t *testing.T) {
	src := "package p\n\nfunc use(s *Server) {\n\ts.run()\n}\n"
	if !resolveScenario(t, impact2164Old, impact2164New, src) {
		t.Fatal("variable-receiver call on removed method must be a typed hit (#2164)")
	}
}

func TestImpact2164ValueReceiverAndMethodValue(t *testing.T) {
	valueRecv := "package p\n\nfunc use(s Server) {\n\ts.run()\n}\n"
	if !resolveScenario(t, impact2164Old, impact2164New, valueRecv) {
		t.Fatal("value-receiver call on removed method must be a typed hit")
	}
	methodVal := "package p\n\nfunc use(s *Server) {\n\tf := s.run\n\tf()\n}\n"
	if !resolveScenario(t, impact2164Old, impact2164New, methodVal) {
		t.Fatal("method value on removed method must be a typed hit")
	}
}

func TestImpact2164UnrelatedTypeSameMethodNameZeroFP(t *testing.T) {
	src := "package p\n\ntype Client struct{}\n\nfunc (c *Client) run() {}\n\nfunc use(c *Client) {\n\tc.run()\n}\n"
	if resolveScenario(t, impact2164Old, impact2164New, src) {
		t.Fatal("same-named method on unrelated type must stay a miss (zero-FP invariant)")
	}
}

func TestImpact2164InterfaceReceiverStaysMiss(t *testing.T) {
	src := "package p\n\ntype Runner interface {\n\trun()\n}\n\nfunc use(r Runner) {\n\tr.run()\n}\n"
	if resolveScenario(t, impact2164Old, impact2164New, src) {
		t.Fatal("interface-typed receiver must stay a conservative miss")
	}
}

func TestImpact2164TypeFullyRemovedStaysMiss(t *testing.T) {
	newAllGone := "package p\n\nfunc other() {}\n"
	src := "package p\n\nfunc use(s *Server) {\n\ts.run()\n}\n"
	if resolveScenario(t, impact2164Old, newAllGone, src) {
		t.Fatal("unresolvable receiver (type removed) must stay a miss, not panic")
	}
}

func TestImpact2164GenericOwnerHit(t *testing.T) {
	// extractImpactRemovedSymbols does not model generic receivers, so build
	// the name tables manually and verify the typed resolution path only.
	// (extracted from: func (b *Box[T]) run() {} removed from Box)
	names := map[string]bool{"run": true}
	owners := map[string]map[string]bool{"run": {"Box": true}}
	fset := token.NewFileSet()
	newSrc := "package p\n\ntype Box[T any] struct{}\n"
	src := "package p\n\nfunc use() {\n\tvar b Box[int]\n\tb.run()\n}\n"
	sib, err := parser.ParseFile(fset, "b.go", src, 0)
	if err != nil {
		t.Fatalf("sibling parse: %v", err)
	}
	nf, err := parser.ParseFile(fset, "a.go", newSrc, 0)
	if err != nil {
		t.Fatalf("edited parse: %v", err)
	}
	found, near := walkImpactRefs(sib, names, owners)
	if found {
		t.Fatal("variable receiver must not be a syntax-level hit")
	}
	if len(near) == 0 {
		t.Fatal("expected a near-miss candidate for b.run()")
	}
	infos := impactTypeCheckGroups(fset, []*ast.File{sib, nf})
	hit := false
	for _, m := range near {
		if impactResolveNearMiss(infos["p"], m, owners) {
			hit = true
		}
	}
	if !hit {
		t.Fatal("generic instantiated receiver must resolve to named type owner")
	}
}

func TestImpact2164ChainReceiverHit(t *testing.T) {
	oldSrc := "package p\n\ntype Server struct{}\n\nfunc (s *Server) run() {}\n\ntype Hub struct{ srv Server }\n"
	newSrc := "package p\n\ntype Server struct{}\n\ntype Hub struct{ srv Server }\n"
	src := "package p\n\nfunc use(h Hub) {\n\th.srv.run()\n}\n"
	if !resolveScenario(t, oldSrc, newSrc, src) {
		t.Fatal("chained selector receiver must resolve via field type")
	}
}

func TestImpact2164NilAndUnresolvedInputsSafe(t *testing.T) {
	owners := map[string]map[string]bool{"run": {"Server": true}}
	if impactResolveNearMiss(nil, impactNearMiss{sel: "run"}, owners) {
		t.Fatal("nil info must stay a miss")
	}
	infos := impactTypeCheckGroups(token.NewFileSet(), nil)
	if infos != nil {
		t.Fatal("no files must yield nil infos")
	}
}
