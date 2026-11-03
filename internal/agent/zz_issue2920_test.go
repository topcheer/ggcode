package agent

import "testing"

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
