package agent

import (
	"strings"
	"testing"
)

// Regression tests for issue #316: shadow_check false positives.

func TestCheckVarShadowing_NoFP_PlainTopLevelRangeLoop(t *testing.T) {
	// Issue #316 defect 1: a plain top-level range loop's key/value must not
	// be flagged as self-shadowing.
	old := ""
	new := `package main

func process(items []string) {
	for i, v := range items {
		_ = i
		_ = v
	}
}
`
	warnings := checkVarShadowing("main.go", old, new)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for plain top-level range loop, got: %v", warnings)
	}
}

func TestCheckVarShadowing_NoFP_SiblingRangeLoops(t *testing.T) {
	// Issue #316 defect 2b: declarations in a sibling statement (first range
	// loop) must not leak into the scope of later sibling statements.
	old := ""
	new := `package main

func process(a, b []string) {
	for i, v := range a {
		_ = i
		_ = v
	}
	for i, v := range b {
		_ = i
		_ = v
	}
}
`
	warnings := checkVarShadowing("main.go", old, new)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for sibling range loops, got: %v", warnings)
	}
}

func TestCheckVarShadowing_NoFP_NestedBlockErrRedeclaration(t *testing.T) {
	// Issue #316 defect 2a: `m, err := h()` after `n, err := g()` in the same
	// nested block is a legal Go redeclaration (err is reused/assigned), not
	// shadowing. "Error variable shadowing ... silently lost" is the opposite
	// of the true semantics.
	old := ""
	new := `package main

func process() error {
	if true {
		n, err := g()
		if err != nil {
			return err
		}
		m, err := h()
		if err != nil {
			return err
		}
		_ = n
		_ = m
	}
	return nil
}
`
	warnings := checkVarShadowing("main.go", old, new)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for legal err redeclaration, got: %v", warnings)
	}
}

func TestCheckVarShadowing_StillDetects_RealRangeShadow(t *testing.T) {
	// Guard: the fix must not suppress REAL shadowing — an outer variable
	// declared before the loop and shadowed by the range variable.
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
		t.Fatal("expected shadowing warning for real range shadow of i")
	}
}

func TestCheckVarShadowing_StillDetects_OuterErrShadowInBlock(t *testing.T) {
	// Guard: a name inherited from an OUTER scope used with := in a nested
	// block is always a new shadowing variable, regardless of the fix.
	old := ""
	new := `package main

func process() error {
	err := doSomething()
	if true {
		err := validate() // shadows outer err
		_ = err
	}
	return nil
}
`
	warnings := checkVarShadowing("main.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected shadowing warning for err shadowing outer scope err")
	}
}

// #325: a var declaration in the same block counts as an existing
// declaration for := reuse — `var n int; ...; n, err := g()` must not warn.
func TestCheckVarShadowing_NoFP_VarDeclThenDefineReuse(t *testing.T) {
	newSrc := `package main

func f(cond bool) error {
	if cond {
		var buf []byte
		buf, err := readAll()
		if err != nil {
			return err
		}
		_ = buf
	}
	return nil
}

func readAll() ([]byte, error) { return nil, nil }
`
	warnings := checkVarShadowing("main.go", "", newSrc)
	for _, w := range warnings {
		if strings.Contains(w, "buf") {
			t.Errorf("false positive: buf reuse after var decl reported: %v", w)
		}
	}
}

// Control: var in outer scope + := inside a *nested* block still reports.
func TestCheckVarShadowing_StillDetects_VarOuterShadow(t *testing.T) {
	newSrc := `package main

func f(cond bool) {
	var n int = 1
	if cond {
		n, err := g()
		_ = n
		_ = err
	}
}

func g() (int, error) { return 0, nil }
`
	warnings := checkVarShadowing("main.go", "", newSrc)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "n") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected real shadowing of n to still be reported, got: %v", warnings)
	}
}
