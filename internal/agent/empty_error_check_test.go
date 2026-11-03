package agent

import (
	"testing"
)

func TestCheckEmptyErrorBody_DetectsEmptyIfErrBody(t *testing.T) {
	old := `package main

func foo() error {
	return nil
}
`
	new := `package main

import "fmt"

func bar() error {
	err := doSomething()
	if err != nil {
	}
	return nil
}

func doSomething() error {
	return nil
}
`
	warnings := checkEmptyErrorBody("test.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected empty error body warning")
	}
	if warnings[0] == "" {
		t.Fatal("expected non-empty warning message")
	}
}

func TestCheckEmptyErrorBody_DetectsNilCheckOnlyEmptyStmts(t *testing.T) {
	old := ""
	new := `package main

func bar() error {
	err := doSomething()
	if err != nil {
		;
	}
	return nil
}
`
	warnings := checkEmptyErrorBody("test.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for body with only empty statements")
	}
}

func TestCheckEmptyErrorBody_NoWarningForNonEmptyBody(t *testing.T) {
	old := ""
	new := `package main

import "fmt"

func bar() error {
	err := doSomething()
	if err != nil {
		fmt.Println(err)
		return err
	}
	return nil
}
`
	warnings := checkEmptyErrorBody("test.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got: %v", warnings)
	}
}

func TestCheckEmptyErrorBody_DeltaAware(t *testing.T) {

	old := `package main

func bar() error {
	err := doSomething()
	if err != nil {
	}
	return nil
}
`

	new := `package main

func bar() error {
	err := doSomething()
	if err != nil {
	}
	return nil
}

func baz() error {
	err := doSomething()
	if err != nil {
	}
	return nil
}
`
	warnings := checkEmptyErrorBody("test.go", old, new)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 new warning (delta), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckEmptyErrorBody_NoNewEmptyBodies(t *testing.T) {
	old := `package main

func bar() error {
	err := doSomething()
	if err != nil {
	}
	return nil
}
`

	warnings := checkEmptyErrorBody("test.go", old, old)
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for unchanged content, got %v", warnings)
	}
}

func TestCheckEmptyErrorBody_NilCheckVar(t *testing.T) {

	old := ""
	new := `package main

func bar() error {
	var readErr error
	if readErr != nil {
	}
	return nil
}
`
	warnings := checkEmptyErrorBody("test.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for readErr empty body")
	}
}

func TestCheckEmptyErrorBody_EqNilCheck(t *testing.T) {

	old := ""
	new := `package main

func bar() {
	err := doSomething()
	if err == nil {
	}
}
`
	warnings := checkEmptyErrorBody("test.go", old, new)
	if len(warnings) > 0 {
		t.Fatalf("expected no warning for 'if err == nil {}' empty body, got: %v", warnings)
	}
}

func TestCheckEmptyErrorBody_NotAnErrorCheck(t *testing.T) {

	old := ""
	new := `package main

func bar() {
	x := 5
	if x != nil {
	}
}
`
	warnings := checkEmptyErrorBody("test.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for non-error nil check, got %v", warnings)
	}
}

func TestCheckEmptyErrorBody_EmptyNewContent(t *testing.T) {
	warnings := checkEmptyErrorBody("test.go", "old stuff", "")
	if warnings != nil {
		t.Fatalf("expected nil for empty content, got %v", warnings)
	}
}

func TestCheckEmptyErrorBody_InvalidGoSyntax(t *testing.T) {
	warnings := checkEmptyErrorBody("test.go", "", "this is not valid go code")
	if warnings != nil {
		t.Fatalf("expected nil for unparseable content, got %v", warnings)
	}
}

func TestCheckEmptyErrorBody_MaxWarnings(t *testing.T) {

	old := ""
	new := `package main

func f1() error {
	err := nil
	if err != nil {
	}
	return nil
}

func f2() error {
	err := nil
	if err != nil {
	}
	return nil
}

func f3() error {
	err := nil
	if err != nil {
	}
	return nil
}

func f4() error {
	err := nil
	if err != nil {
	}
	return nil
}

func f5() error {
	err := nil
	if err != nil {
	}
	return nil
}
`
	warnings := checkEmptyErrorBody("test.go", old, new)
	if len(warnings) != 3 {
		t.Fatalf("expected max 3 warnings, got %d", len(warnings))
	}
}

func TestIsErrorNilCheck(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want bool
	}{
		{"err neq nil", "package x\nfunc f() {\n if err != nil {}\n}", true},
		{"err eq nil", "package x\nfunc f() {\n if err == nil {}\n}", false},
		{"non-error", "package x\nfunc f() {\n if x != nil {}\n}", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := findEmptyErrorBodies(tt.src)

			if tt.want && len(issues) == 0 {
				t.Errorf("expected detection but got none")
			}
			if !tt.want && len(issues) > 0 {
				t.Errorf("expected no detection but got %d", len(issues))
			}
		})
	}
}

func TestFindEmptyErrorBodiesCommentSuppressionExempt(t *testing.T) {
	src := `package p

import "os"

func f() error {
	_, err := os.Open("x")
	if err != nil {
		// intentionally ignored: best-effort probe
	}
	return nil
}
`
	if got := findEmptyErrorBodies(src); len(got) != 0 {
		t.Fatalf("documented suppression still reported: %+v", got)
	}

	bare := `package p

import "os"

func g() error {
	_, err := os.Open("x")
	if err != nil {
	}
	return nil
}
`
	if got := findEmptyErrorBodies(bare); len(got) == 0 {
		t.Fatal("undocumented empty error body no longer detected")
	}
}
