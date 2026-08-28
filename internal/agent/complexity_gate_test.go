package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/codehealth"
)

func TestComplexityGate_NoGoFiles(t *testing.T) {
	a := &Agent{complexityGate: newComplexityGateState()}
	stats := &RunStats{
		FilesEdited: []string{"foo.py", "bar.ts"},
	}
	if msg := a.checkComplexityGate(stats); msg != "" {
		t.Fatalf("expected empty message for non-Go files, got: %s", msg)
	}
}

func TestComplexityGate_AlreadyFired(t *testing.T) {
	a := &Agent{
		complexityGate: &complexityGateState{fired: true},
	}
	stats := &RunStats{
		FilesEdited: []string{"some.go"},
	}
	if msg := a.checkComplexityGate(stats); msg != "" {
		t.Fatalf("expected empty when gate already fired, got: %s", msg)
	}
}

func TestComplexityGate_NoFiles(t *testing.T) {
	a := &Agent{complexityGate: newComplexityGateState()}
	stats := &RunStats{}
	if msg := a.checkComplexityGate(stats); msg != "" {
		t.Fatalf("expected empty for no files edited, got: %s", msg)
	}
}

func TestComplexityGate_HighComplexityDetected(t *testing.T) {
	tmp := t.TempDir()
	goFile := filepath.Join(tmp, "complex.go")

	// Write a Go file with a high-complexity function (many if-statements).
	content := `package testpkg

func ComplexFunc(a, b, c, d int) int {
	if a > 0 && b > 0 || c > 0 {
		return 1
	}
	if a < 0 && b < 0 {
		return 2
	}
	if c < 0 || d < 0 {
		return 3
	}
	if a == b && b == c {
		return 4
	}
	if c == d || a == d {
		return 5
	}
	if a > 100 {
		return 6
	}
		if b > 100 {
			return 7
		}
		if c > 100 {
			return 8
		}
		if d > 100 {
			return 9
		}
		if d > 200 && a > 200 {
			return 10
		}
		if d < 0 && b < 0 {
			return 11
		}
		return 0
	}
`
	if err := os.WriteFile(goFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	a := &Agent{
		complexityGate: newComplexityGateState(),
		workingDir:     tmp,
	}
	stats := &RunStats{
		FilesEdited: []string{goFile},
	}

	msg := a.checkComplexityGate(stats)
	if msg == "" {
		t.Fatal("expected non-empty message for high-complexity function")
	}
	if !a.complexityGate.fired {
		t.Fatal("expected gate to be marked as fired")
	}
	// Should mention the function name or file.
	if !contains(msg, "ComplexFunc") && !contains(msg, "complex.go") {
		t.Errorf("message should mention function or file, got: %s", msg)
	}
}

func TestComplexityGate_LowComplexityNoWarning(t *testing.T) {
	tmp := t.TempDir()
	goFile := filepath.Join(tmp, "simple.go")

	content := `package testpkg

func SimpleFunc(a int) int {
	return a + 1
}
`
	if err := os.WriteFile(goFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	a := &Agent{
		complexityGate: newComplexityGateState(),
		workingDir:     tmp,
	}
	stats := &RunStats{
		FilesEdited: []string{goFile},
	}

	msg := a.checkComplexityGate(stats)
	if msg != "" {
		t.Fatalf("expected empty for simple function, got: %s", msg)
	}
	if a.complexityGate.fired {
		t.Fatal("gate should not fire for simple function")
	}
}

func TestComplexityGate_FiresOnlyOnce(t *testing.T) {
	tmp := t.TempDir()
	goFile := filepath.Join(tmp, "complex.go")

	content := `package testpkg

func ComplexFunc(a, b, c, d int) int {
	if a > 0 && b > 0 || c > 0 {
		return 1
	}
	if a < 0 && b < 0 {
		return 2
	}
	if c < 0 || d < 0 {
		return 3
	}
	if a == b && b == c {
		return 4
	}
	if c == d || a == d {
		return 5
	}
	if a > 100 {
		return 6
	}
		if b > 100 {
			return 7
		}
		if c > 100 {
			return 8
		}
		if d > 100 {
			return 9
		}
		if d > 200 && a > 200 {
			return 10
		}
		if d < 0 && b < 0 {
			return 11
		}
		return 0
	}
`
	if err := os.WriteFile(goFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	a := &Agent{
		complexityGate: newComplexityGateState(),
		workingDir:     tmp,
	}
	stats := &RunStats{
		FilesEdited: []string{goFile},
	}

	msg1 := a.checkComplexityGate(stats)
	if msg1 == "" {
		t.Fatal("expected non-empty on first call")
	}

	msg2 := a.checkComplexityGate(stats)
	if msg2 != "" {
		t.Fatal("expected empty on second call (gate already fired)")
	}
}

func TestComplexityGate_TestFilesSkipped(t *testing.T) {
	a := &Agent{complexityGate: newComplexityGateState()}
	stats := &RunStats{
		FilesEdited: []string{"foo_test.go"},
	}
	if msg := a.checkComplexityGate(stats); msg != "" {
		t.Fatalf("expected empty for test files, got: %s", msg)
	}
}

func TestFilterGoSourceFiles(t *testing.T) {
	input := []string{
		"foo.go",
		"bar_test.go",
		"baz.py",
		"qux.ts",
		"deep/nested.go",
		"skip_test.go",
	}
	result := filterGoSourceFiles(input)
	expected := []string{"foo.go", "deep/nested.go"}
	if len(result) != len(expected) {
		t.Fatalf("expected %d files, got %d: %v", len(expected), len(result), result)
	}
	for i, e := range expected {
		if result[i] != e {
			t.Errorf("expected[%d]=%s, got %s", i, e, result[i])
		}
	}
}

func TestIsComplexityHotspot(t *testing.T) {
	tests := []struct {
		name     string
		complex  int
		length   int
		nesting  int
		expected bool
	}{
		{"healthy", 5, 20, 2, false},
		{"high complexity", 25, 30, 3, true},
		{"too long", 5, 100, 2, true},
		{"too nested", 5, 20, 7, true},
		{"at threshold", 15, 50, 3, true},
		{"just under", 14, 79, 5, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := codehealth.FuncMetrics{
				Complexity:   tt.complex,
				Length:       tt.length,
				NestingDepth: tt.nesting,
			}
			if got := isComplexityHotspot(fn); got != tt.expected {
				t.Errorf("isComplexityHotspot() = %v, want %v", got, tt.expected)
			}
		})
	}
}
