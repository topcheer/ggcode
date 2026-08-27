package agent

import (
	"strings"
	"testing"
)

// ===========================================================================
// #1126: map_prealloc_check.go - nested conditional skips were invisible
//
// hasConditionalSkip only inspected the top level of the loop body while
// scanForMapWrite recursed to any depth, so a guarded write inside a nested
// block was treated as unconditional and preallocation hints fired for code
// that legitimately conditionally populates a map.
// ===========================================================================

const issue1126NestedSkip = `package main

func run(items []string) map[string]string {
	m := make(map[string]string)
	for _, it := range items {
		if it != "" {
			for _, c := range it {
				if c == 'x' {
					m[it] = string(c)
				}
			}
		}
	}
	return m
}
`

func TestMapPreallocNestedConditionalSkipDetected_Issue1126(t *testing.T) {
	result := strings.Join(checkMapPrealloc("test.go", "", issue1126NestedSkip), "\n")
	if result != "" {
		t.Fatalf("#1126: expected no hint (map writes are guarded by nested ifs), got: %s", result)
	}
}

func TestMapPreallocUnconditionalWriteStillFlags_Issue1126(t *testing.T) {
	code := `package main

func run(items []string) map[string]string {
	m := make(map[string]string)
	for _, it := range items {
		m[it] = strings.ToUpper(it)
	}
	return m
}
`
	result := strings.Join(checkMapPrealloc("test.go", "", code), "\n")
	if result == "" {
		t.Fatal("#1126: unconditional fill should still produce a hint")
	}
}

// ===========================================================================
// #1127: nil_deref_check.go - block clearing via defer inside the Inspect
// callback was dead code: the deferred function ran when the callback
// returned, so blockDepth never grew past 0 and nilRisk was never cleared
// per nested block. The rewritten walker pairs explicit enter/exit around
// each BlockStmt. The test below locks in the intended #1070 behavior:
// risk state created inside a block must not leak into sibling blocks.
// ===========================================================================

func TestNilDerefBlockExitClearsRiskRealWalker_Issue1127(t *testing.T) {
	code := `package main

import "fmt"

type Item struct{ Name string }

func src1() (*Item, error) { return nil, fmt.Errorf("e1") }
func src2() (*Item, error) { return &Item{Name: "ok"}, nil }

func use() {
	{
		a, err := src1()
		fmt.Println(a.Name) // BUG: unguarded deref inside inner block
		_ = err
	}
	fmt.Println("between blocks")
	b, err2 := src2()
	if err2 != nil {
		return
	}
	fmt.Println(b.Name) // SAFE: b checked and unrelated to a
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	warnCount := strings.Count(result, "[nil-deref-after-error]")
	if warnCount != 1 {
		t.Fatalf("#1127: want exactly 1 warning (only 'a'), got %d: %s", warnCount, result)
	}
	if !strings.Contains(result, "a") {
		t.Fatalf("#1127: warning should reference 'a', got: %s", result)
	}
}

func TestNilDerefNestedClosureKeepsOwnScope_Issue1127(t *testing.T) {
	code := `package main

import "fmt"

type Pair struct{ Key string }

func gen() (*Pair, error) { return nil, fmt.Errorf("gen") }

func outer() {
	h := func() {
		p, err := gen()
		if err != nil {
			return
		}
		fmt.Println(p.Key)
	}
	h()

	q, err2 := gen()
	if err2 != nil {
		return
	}
	fmt.Println(q.Key) // SAFE: own guard; closure state must not interfere
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result != "" {
		t.Fatalf("#1127: safe pattern flagged after walker rewrite: %s", result)
	}
}

// ===========================================================================
// #1128: nil_deref_check.go - the delta key used file:line:varName, which is
// line-anchored. Inserting lines above the function shifted every line
// number, made new positions differ from old ones and re-reported the same,
// unchanged finding as if it were brand new. Keys now anchor on
// position-independent content (function name + normalized expression path).
// ===========================================================================

const issue1128Base = `package main

import "fmt"

type Result struct {
	Field string
}

func process() (*Result, error) {
	return nil, fmt.Errorf("fail")
}
`

const issue1128WithPad = `package main

import "fmt"

// new explanatory comment block added above the logic
// spanning several lines to shift everything below
type Result struct {
	Field string
}

func process() (*Result, error) {
	return nil, fmt.Errorf("fail")
}
`

const issue1128Use = `
func use() {
	r, err := process()
	fmt.Println(r.Field)
}
`

func TestNilDerefDeltaKeyIgnoresLineShiftAbove_Issue1128(t *testing.T) {
	codeOld := issue1128WithPad + issue1128Use
	codeNew := issue1128Base + issue1128Use

	first := checkNilDerefAfterError("test.go", "", codeNew)
	if first == "" {
		t.Fatal("#1128: baseline content must produce a warning first")
	}

	// Deleting lines ABOVE (old -> new shifted every line number up): the
	// identical pre-existing finding must NOT be re-reported.
	repeat := checkNilDerefAfterError("test.go", codeOld, codeNew)
	if repeat != "" {
		t.Fatalf("#1128: line shift above must not resurrect suppressed warning, got: %s", repeat)
	}
}

func TestNilDerefDeltaKeyStillReportsGenuinelyNewSite_Issue1128(t *testing.T) {
	codeOld := issue1128Base + "\nfunc use(r *Result) {\n\tr.Field = \"x\"\n}\n"
	codeNew := issue1128Base + `
func extra() {
	r2, err := process()
	_ = r2.Field // genuinely new unsafe deref
	_ = err
}
`
	got := checkNilDerefAfterError("test.go", codeOld, codeNew)
	if got == "" || !strings.Contains(got, "r2") {
		t.Fatalf("#1128: a new dereference site must still be reported, got: %q", got)
	}
}

// ===========================================================================
// #1129: nil_map_write_check.go - collectNilRiskMaps deleted nil-risk from
// ANY var-map declaration found order-independently, including the one after
// an earlier write, silently dropping real write-before-make panics with zero
// warnings. Collection and initialization effects are now processed strictly
// in statement order.
// ===========================================================================

func TestNilMapWriteBeforeMakeReported_Issue1129(t *testing.T) {
	code := `package main

func broken() {
	var m map[string]int
	m["count"] = 1   // PANIC: assignment to entry in nil map happens BEFORE make
	m = make(map[string]int)
	m["other"] = 2   // safe
}
`
	result := checkNilMapWrite("test.go", "", code)
	if result == "" {
		t.Fatal("#1129: write-before-make panic site must be reported")
	}
	if !strings.Contains(result, "m") {
		t.Fatalf("#1129: report should reference map 'm', got: %s", result)
	}
	if n := strings.Count(result, "[nil-map-write]"); n != 1 {
		t.Fatalf("#1129: exactly 1 site expected (the pre-make write), got %d: %s", n, result)
	}
}

func TestNilMapWriteMakeFirstStillSilent_Issue1129(t *testing.T) {
	code := `package main

func fine() {
	var m map[string]int
	m = make(map[string]int)
	m["ok"] = 1
}
`
	result := checkNilMapWrite("test.go", "", code)
	if result != "" {
		t.Fatalf("#1129: make-before-write must stay silent, got: %s", result)
	}
}

// ===========================================================================
// #1130: nil_map_write_check.go - any plain assignment counted as
// initialization, so `m = nil` (which returns the variable to its nil zero
// value) permanently marked the map initialized and later writes went
// unreported.
// ===========================================================================

func TestNilMapWriteAfterResetToNilReported_Issue1130(t *testing.T) {
	code := `package main

func resetThenBreak(flag bool) {
	var m map[string]int
	m["first"] = 1          // PANIC #1
	m = make(map[string]int)
	m["second"] = 2         // safe
	if flag {
		m = nil             // revoke initialization (#1130)
	}
	m["third"] = 3          // PANIC #2: back to nil map
}
`
	result := checkNilMapWrite("test.go", "", code)
	if result == "" {
		t.Fatal("#1130: writes after m = nil reset must be reported")
	}
	if n := strings.Count(result, "- test.go:"); n < 2 {
		t.Fatalf("#1130: both panic sites expected (got %d): %s", n, result)
	}
}
