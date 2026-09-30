package agent

// #2920: collectFuncSliceDecls built one flat name-keyed decl map for the
// whole function unit with unconditional last-write-wins, ignoring lexical
// scoping. A nested shadowing declaration whose capability flag differed
// from the outer declaration flipped the entry: false positive on correctly
// preallocated loops (Trigger A) or missed detection on zero-cap ones
// (Trigger B). The fix merges conflicting same-name declarations
// conservatively as hasMakeCapacity=true (suppress the advisory).

import "testing"

// Trigger A: outer slice preallocated via make, inner block legally shadows
// with a zero-cap composite literal. The loop appends to the OUTER
// (preallocated) slice, so no advisory may fire.
func TestIssue2920ShadowedZeroCapLiteralNoFalsePositive(t *testing.T) {
	code := `package main
func g(items []int, slow bool) []int {
	out := make([]int, 0, len(items))
	for _, x := range items {
		out = append(out, x)
	}
	if slow {
		out := []int{}
		_ = out
	}
	return out
}
`
	if got := checkMissingPrealloc("test.go", "", code); len(got) != 0 {
		t.Errorf("preallocated loop with legal nested shadow must not warn, got: %v", got)
	}
}

// Trigger A via the var-decl path: same shape but the shadow is a plain
// `var out []int` inside the nested block.
func TestIssue2920ShadowedVarDeclNoFalsePositive(t *testing.T) {
	code := `package main
func g(items []int, cond bool) []int {
	out := make([]int, 0, len(items))
	for _, x := range items {
		out = append(out, x)
	}
	if cond {
		var out []int
		_ = out
	}
	return out
}
`
	if got := checkMissingPrealloc("test.go", "", code); len(got) != 0 {
		t.Errorf("var-decl shadow must not flip the preallocated entry, got: %v", got)
	}
}

// Trigger B counterpart: outer zero-cap + nested make-capable shadow. The
// conservative merge treats the name as having capacity, so this is
// deliberately NOT flagged (accepted under-report; pinned here so a future
// scope-aware rewrite must consciously revisit this semantics).
func TestIssue2920ConflictingShadowConservativelySuppressed(t *testing.T) {
	code := `package main
func g(items []int, cond bool) []int {
	out := []int{}
	for _, x := range items {
		out = append(out, x)
	}
	if cond {
		out := make([]int, 0, len(items))
		_ = out
	}
	return out
}
`
	if got := checkMissingPrealloc("test.go", "", code); len(got) != 0 {
		t.Errorf("conflicting same-name declarations must suppress conservatively, got: %v", got)
	}
}

// No shadowing: the plain zero-cap loop keeps its advisory (detector teeth).
func TestIssue2920UnshadowedZeroCapStillFlagged(t *testing.T) {
	code := `package main
func g(items []int) []int {
	out := []int{}
	for _, x := range items {
		out = append(out, x)
	}
	return out
}
`
	if got := checkMissingPrealloc("test.go", "", code); len(got) == 0 {
		t.Error("plain zero-cap append loop must still be flagged")
	}
}
