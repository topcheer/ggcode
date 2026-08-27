package agent

import (
	"strings"
	"testing"
)

func TestCheckMissingPrealloc_ForRangeAppend(t *testing.T) {
	src := `package main
func process(items []int) []int {
	var results []int
	for _, item := range items {
		results = append(results, item*2)
	}
	return results
}`
	warnings := checkMissingPrealloc("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected preallocation warning for append-in-range-loop")
	}
	if !strings.Contains(warnings[0], "results") {
		t.Errorf("expected warning to mention 'results', got: %s", warnings[0])
	}
	if !strings.Contains(warnings[0], "make(") {
		t.Errorf("expected warning to suggest make(), got: %s", warnings[0])
	}
}

func TestCheckMissingPrealloc_ForLoopAppend(t *testing.T) {
	src := `package main
func process(n int) []int {
	var data []int
	for i := 0; i < n; i++ {
		data = append(data, i)
	}
	return data
}`
	warnings := checkMissingPrealloc("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected preallocation warning for append-in-for-loop")
	}
}

func TestCheckMissingPrealloc_ShortDeclAppend(t *testing.T) {
	src := `package main
func process(items []string) []string {
	result := []string{}
	for _, s := range items {
		result = append(result, strings.ToUpper(s))
	}
	return result
}`
	warnings := checkMissingPrealloc("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected preallocation warning for short-decl append-in-loop")
	}
	if !strings.Contains(warnings[0], "result") {
		t.Errorf("expected warning to mention 'result', got: %s", warnings[0])
	}
}

func TestCheckMissingPrealloc_PreallocatedNoWarning(t *testing.T) {
	src := `package main
func process(items []int) []int {
	results := make([]int, 0, len(items))
	for _, item := range items {
		results = append(results, item*2)
	}
	return results
}`
	warnings := checkMissingPrealloc("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warning for preallocated slice, got: %v", warnings)
	}
}

func TestCheckMissingPrealloc_MakeWithCapacityLiteralNoWarning(t *testing.T) {
	src := `package main
func process(items []int) []int {
	results := make([]int, 0, 1000)
	for _, item := range items {
		results = append(results, item*2)
	}
	return results
}`
	warnings := checkMissingPrealloc("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warning for preallocated with literal cap, got: %v", warnings)
	}
}

func TestCheckMissingPrealloc_DeltaAware(t *testing.T) {
	oldSrc := `package main
func process(items []int) []int {
	var results []int
	for _, item := range items {
		results = append(results, item)
	}
	return results
}`
	// Same code, should produce no new warnings (already existed).
	warnings := checkMissingPrealloc("test.go", oldSrc, oldSrc)
	if len(warnings) != 0 {
		t.Fatalf("expected no delta warnings for unchanged code, got: %v", warnings)
	}
}

func TestCheckMissingPrealloc_NewAppendInExistingCode(t *testing.T) {
	oldSrc := `package main
func process(items []int) []int {
	var results []int
	for _, item := range items {
		results = append(results, item)
	}
	return results
}`
	newSrc := `package main
func process(items []int) []int {
	var results []int
	for _, item := range items {
		results = append(results, item)
	}
	return results
}
func processMore(items []string) []string {
	var more []string
	for _, s := range items {
		more = append(more, s)
	}
	return more
}`
	warnings := checkMissingPrealloc("test.go", oldSrc, newSrc)
	if len(warnings) == 0 {
		t.Fatal("expected 1 delta warning for new append-in-loop")
	}
	// Should only flag "more", not "results" (already existed).
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "more") {
			found = true
		}
		if strings.Contains(w, "results") {
			t.Error("should not re-flag existing 'results' variable")
		}
	}
	if !found {
		t.Error("expected warning for new 'more' variable")
	}
}

func TestCheckMissingPrealloc_SameVarNameDifferentFunc(t *testing.T) {
	// #1145: two functions declaring the same local slice name. The delta
	// used a set keyed on varName only, so func A's pre-existing violation
	// masked func B's brand-new one and the new warning was silently dropped.
	oldSrc := `package main
func collectA(items []int) []int {
	var results []int
	for _, item := range items {
		results = append(results, item)
	}
	return results
}`
	newSrc := `package main
func collectA(items []int) []int {
	var results []int
	for _, item := range items {
		results = append(results, item)
	}
	return results
}
func collectB(items []string) []string {
	results := []string{}
	for _, s := range items {
		results = append(results, s)
	}
	return results
}`
	warnings := checkMissingPrealloc("test.go", oldSrc, newSrc)
	// The new func B violation must survive the delta even though the
	// varName "results" already existed in old content.
	if len(warnings) == 0 {
		t.Fatal("expected 1 delta warning for new same-named append-in-loop")
	}
	if len(warnings) > 1 {
		t.Fatalf("expected exactly 1 delta warning (old one suppressed), got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "results") {
		t.Errorf("expected warning to reference 'results', got: %v", warnings[0])
	}
}

func TestCheckMissingPrealloc_NoLoopNoWarning(t *testing.T) {
	src := `package main
func process(item int) []int {
	var results []int
	results = append(results, item)
	return results
}`
	warnings := checkMissingPrealloc("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warning for append outside loop, got: %v", warnings)
	}
}

func TestCheckMissingPrealloc_NonGoFile(t *testing.T) {
	src := `var x = []int{}
for item in items:
    x.append(item)`
	warnings := checkMissingPrealloc("test.py", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for non-Go file, got: %v", warnings)
	}
}

func TestCheckMissingPrealloc_TestFileSkipped(t *testing.T) {
	src := `package main
func process(items []int) []int {
	var results []int
	for _, item := range items {
		results = append(results, item)
	}
	return results
}`
	warnings := checkMissingPrealloc("foo_test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for test file, got: %v", warnings)
	}
}

func TestCheckMissingPrealloc_SyntaxErrorSkipped(t *testing.T) {
	src := `package main
func process(items []int) []int {
	var results []int
	for _, item := range items {
		results = append(results, item
	// missing closing paren and brace
}`
	warnings := checkMissingPrealloc("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for file with syntax errors, got: %v", warnings)
	}
}

func TestCheckMissingPrealloc_MultipleLoops(t *testing.T) {
	src := `package main
func process(a, b []int) ([]int, []int) {
	var xs []int
	for _, v := range a {
		xs = append(xs, v)
	}
	var ys []int
	for _, v := range b {
		ys = append(ys, v)
	}
	return xs, ys
}`
	warnings := checkMissingPrealloc("test.go", "", src)
	if len(warnings) < 2 {
		t.Fatalf("expected 2+ warnings for two unpreallocated loops, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckMissingPrealloc_FunctionCallInitSkipped(t *testing.T) {
	src := `package main
import "strings"
func process(s string) []string {
	parts := strings.Split(s, ",")
	return parts
}`
	warnings := checkMissingPrealloc("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for function-call-initialized slice, got: %v", warnings)
	}
}

func TestCheckMissingPrealloc_EmptyContent(t *testing.T) {
	warnings := checkMissingPrealloc("test.go", "", "")
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for empty content, got: %v", warnings)
	}
}

// Issue #1103 regression: fA leaves a zero-capacity declaration on the
// books; fB uses a DIFFERENT s that arrives as a parameter and fills it.
// The old file-wide bare-name index matched fB's loop writes against fA's
// declaration and emitted a misattributed suggestion about fB's parameter.
func TestCheckMissingPrealloc_NoCrossFunctionCollision(t *testing.T) {
	src := `package main

type Pair struct { K, V int }

func fA() {
	s := make([]int, 0)
	_ = s
}

func fB(pairs []Pair, s []string) {
	for _, p := range pairs {
		s = append(s, string(rune(p.V)))
	}
	_ = s
}
`
	warnings := checkMissingPrealloc("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected zero warnings for cross-function name collision, got: %v", warnings)
	}
}

// Scoped collection must not suppress same-function detection.
func TestCheckMissingPrealloc_SameFunctionStillDetected(t *testing.T) {
	src := `package main

func collect(n int) []int {
	out := make([]int, 0)
	for i := 0; i < n; i++ {
		out = append(out, i)
	}
	return out
}
`
	warnings := checkMissingPrealloc("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected missing-prealloc warning for same-function populate")
	}
	if !strings.Contains(warnings[0], "out") {
		t.Errorf("expected warning about out, got: %s", warnings[0])
	}
}
