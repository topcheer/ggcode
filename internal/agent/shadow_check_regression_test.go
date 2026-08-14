package agent

import "testing"

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
