package agent

import (
	"go/parser"
	"go/token"
	"testing"
)

// #1533: three proof-gap regressions in the concurrent-map detector.
func find1533(t *testing.T, src string) int {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", "package p\n"+src, 0)
	if err != nil {
		t.Fatal(err)
	}
	return len(findConcurrentMapAccess(fset, f))
}

// Case A: package-level var map + goroutine write - the headline
// cross-goroutine registry scenario went silent after #1445.
func Test1533A_PackageLevelVarMapSeeded(t *testing.T) {
	n := find1533(t, `
var counters map[string]int

func worker() {
	go func() { counters["k"] = 1 }()
}`)
	if n != 1 {
		t.Fatalf("package-level map write must fire, got %d", n)
	}
}

// Case B: `var m = make(map...)` - inferred declaration shape.
func Test1533B_VarMakeValueSpec(t *testing.T) {
	n := find1533(t, `
func worker() {
	var m = make(map[string]int)
	go func() { m["k"] = 1 }()
}`)
	if n != 1 {
		t.Fatalf("var m = make(...) write must fire, got %d", n)
	}
}

// Case C: compound assignment and increment shapes.
func Test1533C_CompoundAndIncDec(t *testing.T) {
	n := find1533(t, `
func worker() {
	m := make(map[string]int)
	go func() { m["k"] += 1 }()
}`)
	if n != 1 {
		t.Fatalf("m[k] += v must fire, got %d", n)
	}
	n = find1533(t, `
func worker() {
	m := make(map[string]int)
	go func() { m["k"]++ }()
}`)
	if n != 1 {
		t.Fatalf("m[k]++ must fire, got %d", n)
	}
}

// Slice fan-out (the #1445 core target) must stay quiet.
func Test1533_SliceFanoutStaysQuiet(t *testing.T) {
	n := find1533(t, `
func fan(out []int) {
	for i := range out {
		go func() { out[i] = 1 }()
	}
}`)
	if n != 0 {
		t.Fatalf("slice fan-out must not fire, got %d", n)
	}
}

// hasSync still clears warnings on the new shapes.
func Test1533_SyncStillSuppresses(t *testing.T) {
	n := find1533(t, `
var counters map[string]int
var mu sync.Mutex

func worker() {
	go func() {
		mu.Lock()
		counters["k"]++
		mu.Unlock()
	}()
}`)
	if n != 0 {
		t.Fatalf("sync-guarded write must not fire, got %d", n)
	}
}
