package agent

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestCheckDuplicateCode_NonGoFile(t *testing.T) {
	warnings := checkDuplicateCode("test.py", "", "print('hello')")
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for non-Go file, got %d", len(warnings))
	}
}

func TestCheckDuplicateCode_EmptyContent(t *testing.T) {
	warnings := checkDuplicateCode("test.go", "", "")
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for empty content, got %d", len(warnings))
	}
}

func TestCheckDuplicateCode_NoDuplicates(t *testing.T) {
	src := `package main

func processItems(items []string) []string {
	result := make([]string, 0)
	for _, item := range items {
		if item != "" {
			result = append(result, strings.ToUpper(item))
		}
	}
	return result
}

func validateItems(items []string) bool {
	for _, item := range items {
		if item == "" {
			return false
		}
	}
	return true
}
`
	warnings := checkDuplicateCode("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for non-duplicate functions, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckDuplicateCode_ExactClone(t *testing.T) {
	src := `package main

func processStrings(items []string) []string {
	result := make([]string, 0)
	for _, item := range items {
		if item != "" {
			result = append(result, strings.ToUpper(item))
		}
	}
	return result
}

func transformValues(values []string) []string {
	result := make([]string, 0)
	for _, item := range values {
		if item != "" {
			result = append(result, strings.ToUpper(item))
		}
	}
	return result
}
`
	// Old content only has processStrings, so transformValues is new.
	old := `package main

func processStrings(items []string) []string {
	result := make([]string, 0)
	for _, item := range items {
		if item != "" {
			result = append(result, strings.ToUpper(item))
		}
	}
	return result
}
`
	warnings := checkDuplicateCode("test.go", old, src)
	if len(warnings) == 0 {
		t.Fatal("expected duplicate warning for exact clone")
	}
	if !strings.Contains(warnings[0], "Duplicate code detected") {
		t.Fatalf("unexpected warning: %s", warnings[0])
	}
	if !strings.Contains(warnings[0], "100%") {
		t.Fatalf("expected 100%% similarity, got: %s", warnings[0])
	}
}

func TestCheckDuplicateCode_Type2Clone(t *testing.T) {
	// Same structure, different identifiers - should still be detected.
	src := `package main

func calculateTotal(numbers []int) int {
	sum := 0
	for _, n := range numbers {
		if n > 0 {
			sum += n * 2
		}
	}
	if sum > 100 {
		sum = 100
	}
	return sum
}

func computeSum(values []int) int {
	total := 0
	for _, v := range values {
		if v > 0 {
			total += v * 2
		}
	}
	if total > 100 {
		total = 100
	}
	return total
}
`
	warnings := checkDuplicateCode("test.go", "", src)
	if len(warnings) == 0 {
		t.Fatal("expected duplicate warning for Type 2 clone")
	}
}

func TestCheckDuplicateCode_DeltaAware(t *testing.T) {
	// Both functions exist in old content - should NOT trigger.
	src := `package main

func funcA(items []string) []string {
	result := make([]string, 0)
	for _, item := range items {
		if item != "" {
			result = append(result, strings.ToUpper(item))
		}
	}
	return result
}

func funcB(values []string) []string {
	result := make([]string, 0)
	for _, item := range values {
		if item != "" {
			result = append(result, strings.ToUpper(item))
		}
	}
	return result
}
`
	// Old content is the same as new - both existed before.
	warnings := checkDuplicateCode("test.go", src, src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings when both functions are pre-existing, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckDuplicateCode_TooShort(t *testing.T) {
	// Functions with fewer than 5 statements should not be flagged.
	src := `package main

func getNameA() string {
	return "alice"
}

func getNameB() string {
	return "bob"
}
`
	warnings := checkDuplicateCode("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for short functions, got %d", len(warnings))
	}
}

func TestCheckDuplicateCode_SyntaxError(t *testing.T) {
	src := `package main
func broken( {
`
	warnings := checkDuplicateCode("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings for syntax-errored file, got %d", len(warnings))
	}
}

func TestCheckDuplicateCode_SingleFunction(t *testing.T) {
	src := `package main

func onlyOne(items []string) []string {
	result := make([]string, 0)
	for _, item := range items {
		if item != "" {
			result = append(result, strings.ToUpper(item))
		}
	}
	return result
}
`
	warnings := checkDuplicateCode("test.go", "", src)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings with only one function, got %d", len(warnings))
	}
}

func TestCheckDuplicateCode_MethodClones(t *testing.T) {
	src := `package main

type Server struct{}

func (s *Server) handleGet(items []string) []string {
	result := make([]string, 0)
	for _, item := range items {
		if item != "" {
			result = append(result, strings.ToUpper(item))
		}
	}
	return result
}

func (s *Server) handlePost(values []string) []string {
	result := make([]string, 0)
	for _, item := range values {
		if item != "" {
			result = append(result, strings.ToUpper(item))
		}
	}
	return result
}
`
	old := `package main

type Server struct{}

func (s *Server) handleGet(items []string) []string {
	result := make([]string, 0)
	for _, item := range items {
		if item != "" {
			result = append(result, strings.ToUpper(item))
		}
	}
	return result
}
`
	warnings := checkDuplicateCode("test.go", old, src)
	if len(warnings) == 0 {
		t.Fatal("expected duplicate warning for method clones")
	}
}

func TestComputeSimilarity_Identical(t *testing.T) {
	tokens := []string{"if", "v", "return", "v", "call", ".", "E"}
	sigA := funcSignature{
		tokens:   tokens,
		tokenSet: make(map[string]int),
	}
	for _, tok := range tokens {
		sigA.tokenSet[tok]++
	}
	sigB := funcSignature{
		tokens:   tokens,
		tokenSet: make(map[string]int),
	}
	for _, tok := range tokens {
		sigB.tokenSet[tok]++
	}

	sim := computeSimilarity(sigA, sigB)
	if sim != 1.0 {
		t.Fatalf("expected 1.0 similarity for identical signatures, got %f", sim)
	}
}

func TestComputeSimilarity_Different(t *testing.T) {
	sigA := funcSignature{
		tokenSet: map[string]int{"if": 1, "return": 1, "v": 2},
	}
	sigB := funcSignature{
		tokenSet: map[string]int{"for": 1, "range": 1, "break": 1},
	}

	sim := computeSimilarity(sigA, sigB)
	if sim > 0.1 {
		t.Fatalf("expected low similarity for different signatures, got %f", sim)
	}
}

func TestExtractFuncNames(t *testing.T) {
	src := `package main

func foo() {}
func bar(x int) {}
func (s *Server) handle() {}
func (s Server) process() {}
`
	names := extractFuncNames(src)

	expected := []string{"foo", "bar", "handle", "process"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("expected function name %q to be found", name)
		}
	}
}

func TestNormalizeBodyTokens(t *testing.T) {
	// Verify that structurally similar code produces similar token sequences.
	src1 := `package main
func a(x int) int {
	result := 0
	if x > 0 {
		result = x * 2
	}
	if result > 100 {
		result = 100
	}
	return result
}
`
	src2 := `package main
func b(y int) int {
	total := 0
	if y > 0 {
		total = y * 2
	}
	if total > 100 {
		total = 100
	}
	return total
}
`
	fset1 := token.NewFileSet()
	file1, _ := parser.ParseFile(fset1, "a.go", src1, 0)
	fset2 := token.NewFileSet()
	file2, _ := parser.ParseFile(fset2, "b.go", src2, 0)

	if file1 == nil || file2 == nil {
		t.Fatal("failed to parse test files")
	}

	sigs1 := collectFuncSignatures(fset1, file1)
	sigs2 := collectFuncSignatures(fset2, file2)

	if len(sigs1) == 0 || len(sigs2) == 0 {
		t.Fatal("expected to collect function signatures")
	}

	// Two structurally identical functions (only identifiers differ)
	// should produce identical normalized token sequences.
	if len(sigs1[0].tokens) != len(sigs2[0].tokens) {
		t.Fatalf("expected same token count for structurally identical functions, got %d vs %d",
			len(sigs1[0].tokens), len(sigs2[0].tokens))
	}
}
