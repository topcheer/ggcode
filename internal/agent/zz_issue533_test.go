package agent

// Persistent feature tests for GitHub issue #533.
//
// printf_format_check.go:
//   (A)  Line-drift full re-report: the delta key used the line number, so an
//       unrelated edit above a pre-existing issue shifted its line and the
//       whole issue was re-reported as fresh. Delta is now a per-instance
//       multiset keyed by kind+funcName+detail (#186/#171 idiom), line-free.
//   (B1) `%*d` star width consumed an argument that naive verb counting
//       ignored ("1 format verb(s) but 2 argument(s)" on valid code).
//   (B2) Compile-time literal concatenation ("count: " + "%d\n") was flagged
//       as nonconstant-format; go vet treats it as a constant format string.
//
// nil_deref_check.go:
//   (C1) Explicit `v == nil` value guards were not recognized (only err nil
//       checks were), so `if v == nil { return }; return v.Field` was flagged.
//   (C2) Fallback reassignment (`if err != nil { v = &S{...} }`) never
//       cleared the nil risk; any later dereference was still flagged.

import (
	"strings"
	"testing"
)

// --- printf_format: (A) line drift must not re-report pre-existing issues ---

func TestIssue533_PrintfLineDriftNoReReport(t *testing.T) {
	oldSrc := `package main

import "fmt"

func use(n int) {
	fmt.Printf("%d %d\n", n) // pre-existing verb-count issue at line 7
}
`
	// The only change: an unrelated line inserted above shifts the issue down
	// one line. The delta must not treat the shifted issue as fresh.
	newSrc := `package main

import "fmt"

func use(n int) {
	_ = "unrelated"
	fmt.Printf("%d %d\n", n) // same issue, now drifted to line 8
}
`
	warnings := checkPrintfFormat("test.go", oldSrc, newSrc)
	if len(warnings) != 0 {
		t.Fatalf("line drift must not re-report pre-existing printf issue, got %d: %v", len(warnings), warnings)
	}
}

// (A) regression guard: the #172 fix must stay intact — removing one
// instance while adding a DIFFERENT one still reports the new one.
func TestIssue533_PrintfReplacementStillDetected(t *testing.T) {
	oldSrc := `package main

import "log"

func f(u string) { log.Printf(u) }
`
	newSrc := `package main

import "log"

func f(u string) { log.Print(u) }
func g(v string) { log.Printf(v) }
`
	warnings := checkPrintfFormat("test.go", oldSrc, newSrc)
	if len(warnings) == 0 {
		t.Fatal("replacing one printf bug with a different one must still be reported (#172)")
	}
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 fresh warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "non-constant") {
		t.Fatalf("expected nonconstant-format warning, got: %s", warnings[0])
	}
}

// --- printf_format: (B1) star width ---

func TestIssue533_PrintfStarWidthOK(t *testing.T) {
	src := `package main

import "fmt"

func use(w int, v int) {
	fmt.Printf("%*d\n", w, v) // star consumes the width argument: valid
}
`
	warnings := checkPrintfFormat("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("star-width format must not warn, got %d: %v", len(warnings), warnings)
	}
}

func TestIssue533_PrintfStarWidthPrecisionOK(t *testing.T) {
	src := `package main

import "fmt"

func use(w int, p int, f float64) {
	fmt.Printf("%*.*f\n", w, p, f) // width star + precision star: 3 args
}
`
	warnings := checkPrintfFormat("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("star width+precision format must not warn, got %d: %v", len(warnings), warnings)
	}
}

func TestIssue533_PrintfStarWidthMissingArgStillWarns(t *testing.T) {
	src := `package main

import "fmt"

func use(v int) {
	fmt.Printf("%*d\n", v) // star needs an argument: genuinely wrong
}
`
	warnings := checkPrintfFormat("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("star-width with missing argument must warn once, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "format string has") {
		t.Fatalf("expected verb-count warning, got: %s", warnings[0])
	}
}

// --- printf_format: (B2) literal concatenation ---

func TestIssue533_PrintfLiteralConcatOK(t *testing.T) {
	src := `package main

import "fmt"

func use(n int) {
	fmt.Printf("count: "+"%d\n", n) // compile-time constant concatenation
}
`
	warnings := checkPrintfFormat("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("literal-concat format must not warn, got %d: %v", len(warnings), warnings)
	}
}

func TestIssue533_PrintfLiteralConcatVerbMismatchStillWarns(t *testing.T) {
	src := `package main

import "fmt"

func use(n int) {
	fmt.Printf("count: "+"%d %d\n", n) // 2 verbs but 1 arg: genuinely wrong
}
`
	warnings := checkPrintfFormat("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("literal-concat verb mismatch must warn once, got %d: %v", len(warnings), warnings)
	}
}

// Variable concatenation stays a real nonconstant-format risk.
func TestIssue533_PrintfVariableConcatStillWarns(t *testing.T) {
	src := `package main

import "fmt"

func use(prefix string, n int) {
	fmt.Printf(prefix+"%d\n", n) // prefix is variable: injection risk
}
`
	warnings := checkPrintfFormat("test.go", "", src)
	if len(warnings) != 1 {
		t.Fatalf("variable-concat format must warn once, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "non-constant") {
		t.Fatalf("expected nonconstant-format warning, got: %s", warnings[0])
	}
}

// --- nil_deref: (C1) explicit v == nil guard ---

func TestIssue533_NilDerefValueGuardOK(t *testing.T) {
	code := `package main

type Result struct{ Field int }

func get() (*Result, error) { return nil, nil }

func use() int {
	v, err := get()
	if err != nil {
		return 0
	}
	if v == nil { // explicit value guard: standard defensive pattern
		return 0
	}
	return v.Field
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result != "" {
		t.Fatalf("explicit v==nil terminating guard must clear risk, got: %s", result)
	}
}

// v == nil guard with panic body also terminates.
func TestIssue533_NilDerefValueGuardPanicOK(t *testing.T) {
	code := `package main

import "fmt"

type Result struct{ Field int }

func get() (*Result, error) { return nil, nil }

func use() int {
	v, err := get()
	if err != nil {
		return 0
	}
	if v == nil {
		panic("nil result")
	}
	return v.Field
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result != "" {
		t.Fatalf("v==nil panic guard must clear risk, got: %s", result)
	}
}

// Non-terminating v == nil guard does NOT clear the risk past the guard.
func TestIssue533_NilDerefValueGuardNonTerminatingStillWarns(t *testing.T) {
	code := `package main

import "fmt"

type Result struct{ Field int }

func get() (*Result, error) { return nil, nil }

func use() int {
	v, err := get()
	_ = err
	if v == nil { // non-terminating: does not prove anything afterwards
		fmt.Println("nil")
	}
	return v.Field // guard fell through: v may still be nil
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result == "" {
		t.Fatal("non-terminating v==nil guard must still warn")
	}
	if !strings.Contains(result, "'v'") {
		t.Fatalf("expected warning about 'v', got: %s", result)
	}
}

// `if v != nil { ... v.Field ... }` is safe inside the body.
func TestIssue533_NilDerefValueNotNilBodySafe(t *testing.T) {
	code := `package main

type Result struct{ Field int }

func get() (*Result, error) { return nil, nil }

func use() int {
	v, err := get()
	if err != nil {
		return 0
	}
	if v != nil {
		return v.Field // safe: v verified non-nil in this branch
	}
	return 0
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result != "" {
		t.Fatalf("deref inside v!=nil branch must not warn, got: %s", result)
	}
}

// --- nil_deref: (C2) fallback reassignment ---

func TestIssue533_NilDerefFallbackReassignOK(t *testing.T) {
	code := `package main

type Result struct{ Field int }

func get() (*Result, error) { return nil, nil }

func use() int {
	v, err := get()
	if err != nil {
		v = &Result{Field: 7} // fallback: v cannot be nil afterwards
	}
	return v.Field
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result != "" {
		t.Fatalf("fallback reassignment must clear nil risk, got: %s", result)
	}
}

// `v = nil` assignment must NOT clear the risk.
func TestIssue533_NilDerefExplicitNilAssignStillWarns(t *testing.T) {
	code := `package main

import "fmt"

type Result struct{ Field int }

func get() (*Result, error) { return nil, nil }

func use() int {
	v, err := get()
	if err != nil {
		v = nil // still nil: the risk must survive
	}
	return v.Field
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result == "" {
		t.Fatal("v = nil assignment must keep the nil-deref warning")
	}
}

// Self-referential fallback (`v = v.Next`) keeps the risk.
func TestIssue533_NilDerefSelfRefReassignStillWarns(t *testing.T) {
	code := `package main

import "fmt"

type Node struct{ Next *Node }

func get() (*Node, error) { return nil, nil }

func use() *Node {
	v, err := get()
	if err != nil {
		v = v.Next // reads v itself: not a non-null fallback
	}
	return v.Next
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result == "" {
		t.Fatal("v = v.Next assignment must keep the nil-deref warning")
	}
}

// Fallback reassignment inside an err==nil branch must not become a
// permanent clear (#238 snapshot semantics stay in charge of that scope).
func TestIssue533_NilDerefReassignInsideErrNilBranchStillWarns(t *testing.T) {
	code := `package main

import "fmt"

type Result struct{ Field int }

func get() (*Result, error) { return nil, nil }

func use() int {
	v, err := get()
	if err == nil {
		v = &Result{Field: 7} // only non-nil in THIS branch
		_ = v.Field
	}
	fmt.Println(v.Field) // outside: v may still be nil
	return 0
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result == "" {
		t.Fatal("reassignment inside err==nil branch must not clear risk outside it")
	}
	if !strings.Contains(result, "'v'") {
		t.Fatalf("expected warning about 'v', got: %s", result)
	}
}

// Fallback with new(T) also clears the risk.
func TestIssue533_NilDerefFallbackNewOK(t *testing.T) {
	code := `package main

type Result struct{ Field int }

func get() (*Result, error) { return nil, nil }

func use() int {
	v, err := get()
	if err != nil {
		v = new(Result) // non-null fallback
	}
	return v.Field
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result != "" {
		t.Fatalf("new(T) fallback must clear nil risk, got: %s", result)
	}
}

// Plain call fallback (`v = f()`) keeps the risk — f may return nil.
func TestIssue533_NilDerefCallReassignStillWarns(t *testing.T) {
	code := `package main

import "fmt"

type Result struct{ Field int }

func other() *Result { return nil }

func get() (*Result, error) { return nil, nil }

func use() int {
	v, err := get()
	if err != nil {
		v = other() // may still be nil
	}
	return v.Field
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result == "" {
		t.Fatal("plain-call reassignment must keep the nil-deref warning")
	}
}

// guard and fallback without any err check: still flagged.
func TestIssue533_NilDerefNoGuardStillWarns(t *testing.T) {
	code := `package main

import "fmt"

type Result struct{ Field int }

func get() (*Result, error) { return nil, nil }

func use() int {
	v, err := get()
	fmt.Println(v.Field) // no guard at all
	return 0
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result == "" {
		t.Fatal("unguarded dereference must still warn")
	}
}
