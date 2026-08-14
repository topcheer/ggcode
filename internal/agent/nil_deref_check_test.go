package agent

import (
	"strings"
	"testing"
)

func TestCheckNilDerefAfterError_BasicDereference(t *testing.T) {
	code := `package main

import "fmt"

type Result struct {
	Field string
}

func process() (*Result, error) {
	return nil, fmt.Errorf("fail")
}

func use() {
	r, err := process()
	fmt.Println(r.Field) // BUG: r may be nil
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result == "" {
		t.Fatal("expected nil deref warning, got empty")
	}
	if !strings.Contains(result, "r") {
		t.Fatalf("expected warning about variable 'r', got: %s", result)
	}
}

func TestCheckNilDerefAfterError_CheckedError(t *testing.T) {
	code := `package main

import "fmt"

type Result struct {
	Field string
}

func process() (*Result, error) {
	return &Result{}, nil
}

func use() {
	r, err := process()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(r.Field) // safe: err checked above
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result != "" {
		t.Fatalf("expected no warning when err is checked, got: %s", result)
	}
}

func TestCheckNilDerefAfterError_StarExpr(t *testing.T) {
	code := `package main

import "fmt"

func getInt() (*int, error) {
	return nil, fmt.Errorf("fail")
}

func use() {
	v, err := getInt()
	fmt.Println(*v) // BUG: dereference without check
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result == "" {
		t.Fatal("expected nil deref warning for *v")
	}
	if !strings.Contains(result, "v") {
		t.Fatalf("expected warning about variable 'v', got: %s", result)
	}
}

func TestCheckNilDerefAfterError_IndexExpr(t *testing.T) {
	code := `package main

import "fmt"

func getSlice() ([]int, error) {
	return nil, fmt.Errorf("fail")
}

func use() {
	s, err := getSlice()
	fmt.Println(s[0]) // BUG: index without check
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result == "" {
		t.Fatal("expected nil deref warning for s[0]")
	}
}

func TestCheckNilDerefAfterError_MethodCall(t *testing.T) {
	code := `package main

import "fmt"

type Conn struct{}

func (c *Conn) Read() ([]byte, error) {
	return nil, fmt.Errorf("fail")
}

func getConn() (*Conn, error) {
	return nil, fmt.Errorf("fail")
}

func use() {
	c, err := getConn()
	c.Read() // BUG: method call without check
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result == "" {
		t.Fatal("expected nil deref warning for c.Read()")
	}
}

func TestCheckNilDerefAfterError_DeltaAware(t *testing.T) {
	oldCode := `package main

import "fmt"

type T struct{ V int }
func get() (*T, error) { return nil, nil }

func use() {
	v, err := get()
	fmt.Println(v.V)
}
`
	newCode := oldCode + `
func use2() {
	w, err := get()
	fmt.Println(w.V)
}
`
	result := checkNilDerefAfterError("test.go", oldCode, newCode)
	if result == "" {
		t.Fatal("expected warning for new function use2")
	}
	if !strings.Contains(result, "'w'") {
		t.Fatalf("expected warning about variable 'w', got: %s", result)
	}
	if strings.Contains(result, "v.V") {
		t.Fatalf("should not flag pre-existing variable v, got: %s", result)
	}
}

func TestCheckNilDerefAfterError_SafeAfterCheck(t *testing.T) {
	code := `package main

type Result struct {
	Field string
}

func process() (*Result, error) {
	return &Result{}, nil
}

func use() {
	r, err := process()
	if err != nil {
		return
	}
	_ = r.Field // safe after error check
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result != "" {
		t.Fatalf("expected no warning, got: %s", result)
	}
}

func TestCheckNilDerefAfterError_NonGoFile(t *testing.T) {
	result := checkNilDerefAfterError("test.py", "", "print('hello')")
	if result != "" {
		t.Fatalf("expected empty for non-Go file, got: %s", result)
	}
}

// fix #238: `if err == nil` guard makes the dereference safe inside the body.
func TestCheckNilDerefAfterError_ErrNilCheckSafe(t *testing.T) {
	code := `package main

type Result struct {
	Field string
}

func process() (*Result, error) {
	return &Result{}, nil
}

func use() {
	v, err := process()
	if err == nil {
		_ = v.Field // safe: err verified nil in this branch
	}
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result != "" {
		t.Fatalf("expected no warning for deref inside err==nil branch, got: %s", result)
	}
}

// fix #238: deref inside `if err != nil` body is genuinely dangerous.
func TestCheckNilDerefAfterError_DerefInsideErrNotNilBranch(t *testing.T) {
	code := `package main

type Result struct {
	Field string
}

func process() (*Result, error) {
	return nil, nil
}

func use() {
	v, err := process()
	if err != nil {
		_ = v.Field // BUG: v is likely nil when err != nil
	}
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result == "" {
		t.Fatal("expected warning for deref inside err!=nil branch")
	}
	if !strings.Contains(result, "'v'") {
		t.Fatalf("expected warning about variable 'v', got: %s", result)
	}
}

// fix #238: test files are skipped (doc already claimed this).
func TestCheckNilDerefAfterError_SkipsTestFiles(t *testing.T) {
	code := `package main

import "fmt"

type Result struct {
	Field string
}

func process() (*Result, error) {
	return nil, fmt.Errorf("fail")
}

func use() {
	r, err := process()
	fmt.Println(r.Field) // would be flagged in non-test code
	_ = err
}
`
	result := checkNilDerefAfterError("foo_test.go", "", code)
	if result != "" {
		t.Fatalf("expected empty for test file, got: %s", result)
	}
}

func TestCheckNilDerefAfterError_EmptyContent(t *testing.T) {
	result := checkNilDerefAfterError("test.go", "", "")
	if result != "" {
		t.Fatalf("expected empty for empty content")
	}
}

func TestCheckNilDerefAfterError_NoErrorReturn(t *testing.T) {
	code := `package main

type Result struct {
	Field string
}

func process() *Result {
	return &Result{}
}

func use() {
	r := process()
	_ = r.Field // no error involved
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result != "" {
		t.Fatalf("expected no warning for non-error return, got: %s", result)
	}
}

func TestCheckNilDerefAfterError_CustomErrorName(t *testing.T) {
	code := `package main

import "fmt"

type T struct{ V int }
func get() (*T, error) { return nil, nil }

func use() {
	v, myErr := get()
	fmt.Println(v.V)
	_ = myErr
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result == "" {
		t.Fatal("expected warning for variable with custom error name suffix")
	}
}

func TestCheckNilDerefAfterError_MultipleDerefs(t *testing.T) {
	code := `package main

import "fmt"

type T struct{ V int }
func get() (*T, error) { return nil, nil }

func use() {
	v, err := get()
	fmt.Println(v.V)
	fmt.Println(v.V) // second deref of same var
	_ = err
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result == "" {
		t.Fatal("expected warning")
	}
	// Should only report once per variable.
	count := strings.Count(result, "'v' is dereferenced")
	if count != 1 {
		t.Fatalf("expected 1 warning for v, got %d in: %s", count, result)
	}
}

func TestCheckNilDerefAfterError_MultiErrorVariables(t *testing.T) {
	// Regression test for #97: when multiple error variables exist, checking
	// one should NOT clear nil-risk for variables associated with the other.
	code := `package main

import "fmt"

type T struct{ V int }

func getA() (*T, error) { return nil, nil }
func getB() (*T, error) { return nil, nil }

func use() {
	a, err1 := getA()
	b, err2 := getB()

	if err1 != nil {
		return
	}

	// err2 was NEVER checked, so b could still be nil.
	fmt.Println(b.V) // BUG: should be flagged — err2 not checked
	_ = a.V
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result == "" {
		t.Fatal("expected nil deref warning for 'b' (err2 never checked), got empty")
	}
	if !strings.Contains(result, "'b'") {
		t.Fatalf("expected warning about variable 'b', got: %s", result)
	}
	// 'a' should NOT be flagged since err1 was checked.
	if strings.Contains(result, "'a'") {
		t.Fatalf("should not flag 'a' (err1 was checked), got: %s", result)
	}
}

// TestCheckNilDerefAfterError_LogFatalGuardNoWarn verifies #273: a
// `if err != nil { log.Fatal(err) }` guard is noreturn, so the dereference
// after it must not be flagged.
func TestCheckNilDerefAfterError_LogFatalGuardNoWarn(t *testing.T) {
	code := `package main

import (
	"fmt"
	"log"
)

type T struct{ Field string }

func get() (*T, error) { return nil, nil }

func use() {
	v, err := get()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(v.Field) // safe: log.Fatal never returns
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result != "" {
		t.Fatalf("expected no warning after log.Fatal guard, got: %s", result)
	}
}

// TestCheckNilDerefAfterError_LogFatalfGuardNoWarn verifies #273 for
// log.Fatalf and os.Exit variants.
func TestCheckNilDerefAfterError_NoreturnSelectorVariants(t *testing.T) {
	cases := []struct {
		name  string
		guard string
	}{
		{"log.Fatalf", `log.Fatalf("bad: %v", err)`},
		{"log.Fatalln", `log.Fatalln(err)`},
		{"os.Exit", `os.Exit(1)`},
		{"runtime.Goexit", `runtime.Goexit()`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code := "package main\n\nimport (\n\t\"fmt\"\n\t\"log\"\n\t\"os\"\n\t\"runtime\"\n)\n\ntype T struct{ Field string }\n\nfunc get() (*T, error) { return nil, nil }\n\nfunc use() {\n\tv, err := get()\n\tif err != nil {\n\t\t" + tc.guard + "\n\t}\n\tfmt.Println(v.Field)\n}\n"
			result := checkNilDerefAfterError("test.go", "", code)
			if result != "" {
				t.Fatalf("expected no warning after %s guard, got: %s", tc.name, result)
			}
		})
	}
}

// TestCheckNilDerefAfterError_OrdinaryCallGuardStillWarns verifies #273 does
// not over-generalize: a plain fmt.Println guard falls through, so the
// subsequent dereference is still flagged.
func TestCheckNilDerefAfterError_OrdinaryCallGuardStillWarns(t *testing.T) {
	code := `package main

import "fmt"

type T struct{ Field string }

func get() (*T, error) { return nil, nil }

func use() {
	v, err := get()
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println(v.Field) // BUG: guard does not exit; v may be nil
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result == "" {
		t.Fatal("expected warning after non-terminating fmt.Println guard")
	}
	if !strings.Contains(result, "'v'") {
		t.Fatalf("expected warning about variable 'v', got: %s", result)
	}
}

// TestCheckNilDerefAfterError_NEQReturnElseNoWarn verifies #281: when the
// then branch of `if err != nil` terminates, the risk must stay permanently
// cleared after the else block -- restoreSaved must be skipped.
func TestCheckNilDerefAfterError_NEQReturnElseNoWarn(t *testing.T) {
	code := `package main

import "fmt"

type T struct{ Field string }

func get() (*T, error) { return nil, nil }

func use() {
	v, err := get()
	if err != nil {
		return
	} else {
		_ = v
	}
	fmt.Println(v.Field) // safe: only reachable when err == nil
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result != "" {
		t.Fatalf("expected no warning when then branch returns and else exists (#281), got: %s", result)
	}
}

// TestCheckNilDerefAfterError_NEQNonTerminatingElseRestores verifies #281 keeps
// the restore semantics when the then branch does NOT terminate: risk must be
// restored after walking the else block.
func TestCheckNilDerefAfterError_NEQNonTerminatingElseRestores(t *testing.T) {
	code := `package main

import "fmt"

type T struct{ Field string }

func get() (*T, error) { return nil, nil }

func use() {
	v, err := get()
	if err != nil {
		fmt.Println(err)
	} else {
		_ = v
	}
	fmt.Println(v.Field) // BUG: then branch falls through; v may still be nil
}
`
	result := checkNilDerefAfterError("test.go", "", code)
	if result == "" {
		t.Fatal("expected warning when then branch does not terminate (#281 restore kept)")
	}
	if !strings.Contains(result, "'v'") {
		t.Fatalf("expected warning about variable 'v', got: %s", result)
	}
}
