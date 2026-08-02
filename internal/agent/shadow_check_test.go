package agent

import (
	"strings"
	"testing"
)

func TestCheckVarShadowing_ErrShadowInIfBlock(t *testing.T) {
	old := ""
	new := `package main

func process() error {
	err := doSomething()
	if err != nil {
		return err
	}
	if x > 0 {
		err := validate(x)
		_ = err
	}
	return nil
}
`
	warnings := checkVarShadowing("main.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected shadowing warning for err in if block")
	}
	if !strings.Contains(warnings[0], "err") {
		t.Errorf("expected warning to mention err, got: %s", warnings[0])
	}
	if !strings.Contains(strings.ToLower(warnings[0]), "error") {
		t.Errorf("expected warning to mention error, got: %s", warnings[0])
	}
}

func TestCheckVarShadowing_NoShadow_SameScope(t *testing.T) {
	old := ""
	new := `package main

func process() {
	err := doSomething()
	_ = err
	err = doOther() // reassignment, not shadowing
	_ = err
}
`
	warnings := checkVarShadowing("main.go", old, new)
	if len(warnings) != 0 {
		t.Errorf("expected no shadowing warnings for same-scope reassignment, got: %v", warnings)
	}
}

func TestCheckVarShadowing_NonErrorVar(t *testing.T) {
	old := ""
	new := `package main

func process() {
	x := 10
	if true {
		x := 20 // shadow
		_ = x
	}
	_ = x
}
`
	warnings := checkVarShadowing("main.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected shadowing warning for non-error variable x")
	}
	// Should not have the special error message
	if strings.Contains(warnings[0], "Error variable") {
		t.Errorf("should not flag x as error variable: %s", warnings[0])
	}
}

func TestCheckVarShadowing_DeltaAware(t *testing.T) {
	// Same shadowing in old and new -> no warning.
	src := `package main

func process() error {
	err := doSomething()
	if true {
		err := validate()
		_ = err
	}
	return nil
}
`
	warnings := checkVarShadowing("main.go", src, src)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for pre-existing shadowing, got: %v", warnings)
	}
}

func TestCheckVarShadowing_NewShadowIntroduced(t *testing.T) {
	old := `package main

func process() error {
	err := doSomething()
	_ = err
	return nil
}
`
	new := `package main

func process() error {
	err := doSomething()
	if x > 0 {
		err := validate(x)
		_ = err
	}
	return nil
}
`
	warnings := checkVarShadowing("main.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected shadowing warning for newly introduced err shadow")
	}
}

func TestCheckVarShadowing_NonGoFile(t *testing.T) {
	warnings := checkVarShadowing("main.py", "", "def foo():\n    pass\n")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for non-Go file, got: %v", warnings)
	}
}

func TestCheckVarShadowing_TestFile(t *testing.T) {
	new := `package main

func TestFoo(t *testing.T) {
	err := bar()
	if true {
		err := baz()
		_ = err
	}
	_ = err
}
`
	warnings := checkVarShadowing("main_test.go", "", new)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for test file, got: %v", warnings)
	}
}

func TestCheckVarShadowing_ShadowInForLoop(t *testing.T) {
	old := ""
	new := `package main

func process(items []int) {
	i := 99
	for _, i := range items {
		_ = i
	}
	_ = i
}
`
	warnings := checkVarShadowing("main.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected shadowing warning for i in range loop")
	}
}

func TestCheckVarShadowing_ShadowInSwitchCase(t *testing.T) {
	old := ""
	new := `package main

func process(val int) {
	x := 42
	switch val {
	case 1:
		x := val + 1
		_ = x
	}
	_ = x
}
`
	warnings := checkVarShadowing("main.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected shadowing warning for x in switch case")
	}
}

func TestCheckVarShadowing_ShadowInClosure(t *testing.T) {
	old := ""
	new := `package main

func process() {
	err := doSomething()
	fn := func() {
		err := another() // shadows outer err
		_ = err
	}
	fn()
	_ = err
}
`
	warnings := checkVarShadowing("main.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected shadowing warning for err in closure")
	}
}

func TestCheckVarShadowing_ShadowInTypeSwitch(t *testing.T) {
	old := ""
	new := `package main

func process(v interface{}) {
	x := 42
	switch t := v.(type) {
	case int:
		x := t + 1
		_ = x
	}
	_ = x
}
`
	warnings := checkVarShadowing("main.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected shadowing warning for x in type switch case")
	}
}

func TestCheckVarShadowing_NoFalsePositive_DistinctNames(t *testing.T) {
	old := ""
	new := `package main

func process() {
	a := 1
	if true {
		b := 2
		_ = b
	}
	_ = a
}
`
	warnings := checkVarShadowing("main.go", old, new)
	if len(warnings) != 0 {
		t.Errorf("expected no shadowing warnings for distinct names, got: %v", warnings)
	}
}

func TestCheckVarShadowing_EmptyContent(t *testing.T) {
	warnings := checkVarShadowing("main.go", "", "")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for empty content, got: %v", warnings)
	}
}

func TestCheckVarShadowing_MultipleShadows(t *testing.T) {
	old := ""
	new := `package main

func process() {
	x := 1
	err := foo()
	if true {
		x := 2
		err := bar()
		_ = x
		_ = err
	}
	_ = x
	_ = err
}
`
	warnings := checkVarShadowing("main.go", old, new)
	if len(warnings) < 2 {
		t.Fatalf("expected at least 2 shadowing warnings (err + x), got %d: %v", len(warnings), warnings)
	}
}

func TestCheckVarShadowing_ShadowInSelect(t *testing.T) {
	old := ""
	new := `package main

func process(ch chan int) {
	x := 42
	select {
	case x := <-ch:
		_ = x
	}
	_ = x
}
`
	warnings := checkVarShadowing("main.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected shadowing warning for x in select case")
	}
}
