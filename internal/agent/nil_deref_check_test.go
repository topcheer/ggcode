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
