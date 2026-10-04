package agent

// Regression tests for #3355: the slice prealloc check (Check #56) must not
// flag `var x []T` bindings that are later granted capacity via plain
// assignment (x = make([]T, 0, N)) - including branch-selected capacities -
// while still catching genuinely zero-cap appends.

import "testing"

func TestIssue3355_VarDeclAssignMakeWithCapNoWarning(t *testing.T) {
	src := `package main
func collect(lines []string) []string {
	var files []string
	files = make([]string, 0, len(lines))
	for _, l := range lines {
		files = append(files, l)
	}
	return files
}`
	warnings := checkMissingPrealloc("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warning for var + assign make-with-cap, got: %v", warnings)
	}
}

func TestIssue3355_BranchSelectedCapacityNoWarning(t *testing.T) {
	src := `package main
func collect(lines []string, big bool) []string {
	var files []string
	if big {
		files = make([]string, 0, len(lines)*2)
	} else {
		files = make([]string, 0, len(lines))
	}
	for _, l := range lines {
		files = append(files, l)
	}
	return files
}`
	warnings := checkMissingPrealloc("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warning for branch-selected capacity, got: %v", warnings)
	}
}

func TestIssue3355_AssignZeroCapMakeStillWarns(t *testing.T) {
	// make([]T, 0) grants no capacity: the guard must not over-suppress.
	src := `package main
func collect(lines []string) []string {
	var files []string
	files = make([]string, 0)
	for _, l := range lines {
		files = append(files, l)
	}
	return files
}`
	warnings := checkMissingPrealloc("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected warning to survive for assign make([]T, 0) (no capacity granted)")
	}
}

func TestIssue3355_VarOnlyTruePositiveIntact(t *testing.T) {
	src := `package main
func collect(lines []string) []string {
	var files []string
	for _, l := range lines {
		files = append(files, l)
	}
	return files
}`
	warnings := checkMissingPrealloc("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected warning for plain var-only zero-cap append loop")
	}
}

func TestIssue3355_UpgradeDoesNotLeakAcrossUnits(t *testing.T) {
	// A package-level zero-cap var granted capacity in one function must not
	// silently flip the shared decl seen by a sibling function that appends
	// without any capacity grant - that function still deserves its warning.
	src := `package main

var sink []string

func sized(lines []string) {
	sink = make([]string, 0, len(lines))
	for _, l := range lines {
		sink = append(sink, l)
	}
}

func unsized(lines []string) {
	for _, l := range lines {
		sink = append(sink, l)
	}
}`
	warnings := checkMissingPrealloc("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected warning from unsized() after sibling unit granted capacity")
	}
}
