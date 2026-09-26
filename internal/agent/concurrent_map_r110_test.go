package agent

// r110: unit tests for the analyzeFuncForMapConcurrency decomposition.
// The public behavior contract stays covered by concurrent_map_check_test.go
// (via checkConcurrentMapAccess); these tests pin the helper-level semantics
// that the refactor distributed across step methods:
//   - map-typed params/receivers seed the declaration-proof set
//   - plain-identifier index writes require proof (#1445-A)
//   - sync evidence clears pending writes (#218)
//   - delete() is recorded unconditionally (map operand required to compile)
//   - IncDecStmt on a proven map is flagged (#1533-C)

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// parseFuncR110 parses src and returns its first FuncDecl.
func parseFuncR110(t *testing.T, src string) *ast.FuncDecl {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "t.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok {
			return fn
		}
	}
	t.Fatal("no func decl found")
	return nil
}

func TestAnalyzeFuncForMapConcurrency_ParamProofSeeds(t *testing.T) {
	fn := parseFuncR110(t, `package p
func worker(m map[string]int) {
	go func() { m["k"] = 1 }()
	m["j"] = 2
}`)
	info := analyzeFuncForMapConcurrency(fn, nil)
	if !info.hasGoStatement {
		t.Fatal("expected go statement detected")
	}
	if _, ok := info.unsyncMapWrites["m"]; !ok {
		t.Fatalf("map-typed param write should be flagged, got %v", info.unsyncMapWrites)
	}
}

func TestAnalyzeFuncForMapConcurrency_UnprovenIdentNotWrite(t *testing.T) {
	fn := parseFuncR110(t, `package p
func worker(out []int) {
	go func() {}()
	out[0] = 1
}`)
	info := analyzeFuncForMapConcurrency(fn, nil)
	if len(info.unsyncMapWrites) != 0 {
		t.Fatalf("slice index write must not be flagged: %v", info.unsyncMapWrites)
	}
}

func TestAnalyzeFuncForMapConcurrency_SyncClearsWrites(t *testing.T) {
	fn := parseFuncR110(t, `package p
func worker() {
	var mu sync.Mutex
	m := make(map[string]int)
	go func() { m["k"] = 1 }()
	mu.Lock()
	m["j"] = 2
	_ = mu
}`)
	info := analyzeFuncForMapConcurrency(fn, nil)
	if len(info.unsyncMapWrites) != 0 {
		t.Fatalf("mutex evidence must clear pending writes: %v", info.unsyncMapWrites)
	}
}

func TestAnalyzeFuncForMapConcurrency_DeleteUnconditional(t *testing.T) {
	fn := parseFuncR110(t, `package p
func worker() {
	go func() {}()
	delete(cache, "k")
}`)
	info := analyzeFuncForMapConcurrency(fn, nil)
	if _, ok := info.unsyncMapWrites["cache"]; !ok {
		t.Fatalf("delete() needs no declaration proof (map operand required to compile): %v", info.unsyncMapWrites)
	}
}

func TestAnalyzeFuncForMapConcurrency_IncDecOnProvenMap(t *testing.T) {
	fn := parseFuncR110(t, `package p
func worker() {
	counters := map[string]int{}
	go func() { counters["a"]++ }()
	counters["b"]++
}`)
	info := analyzeFuncForMapConcurrency(fn, nil)
	if _, ok := info.unsyncMapWrites["counters"]; !ok {
		t.Fatalf("index incdec on proven map must be flagged: %v", info.unsyncMapWrites)
	}
}
