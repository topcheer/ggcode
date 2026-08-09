package agent

import (
	"strings"
	"testing"
)

func TestFixAmnesia_BasicDetection(t *testing.T) {
	d := newFixAmnesiaState()

	// Simulate: nil dereference error observed in fileA.go
	d.recordErrorObserved("nil-deref-after-nil-check", "/src/fileA.go")

	// Now check content in fileB.go that matches the pattern
	// The pattern looks for nil check followed by method call outside guard
	content := `
if err != nil {
    return err
}
result.Method()
`
	got := d.checkContentAgainstFixed("/src/fileA.go", "/src/fileB.go", content)
	if got == "" {
		t.Error("expected fix amnesia warning, got empty")
	}
	if !strings.Contains(got, "nil pointer") {
		t.Errorf("expected 'nil pointer' in warning, got: %s", got)
	}
	if !strings.Contains(got, "fileA.go") {
		t.Errorf("expected reference to fileA.go, got: %s", got)
	}
	if !strings.Contains(got, "fileB.go") {
		t.Errorf("expected reference to fileB.go, got: %s", got)
	}
}

func TestFixAmnesia_SameFileNoWarning(t *testing.T) {
	d := newFixAmnesiaState()
	d.recordErrorObserved("nil-deref-after-nil-check", "/src/fileA.go")

	content := `
if err != nil {
    return err
}
result.Method()
`
	// Same file should not trigger (fixing the same file, not amnesia)
	got := d.checkContentAgainstFixed("/src/fileA.go", "/src/fileA.go", content)
	if got != "" {
		t.Errorf("expected no warning for same file, got: %s", got)
	}
}

func TestFixAmnesia_NoPriorFixNoWarning(t *testing.T) {
	d := newFixAmnesiaState()

	// No prior error observed — should not trigger
	content := `
if err != nil {
    return err
}
result.Method()
`
	got := d.checkContentAgainstFixed("", "/src/fileB.go", content)
	if got != "" {
		t.Errorf("expected no warning without prior fix, got: %s", got)
	}
}

func TestFixAmnesia_DifferentCategoryNoWarning(t *testing.T) {
	d := newFixAmnesiaState()
	// Observed nil-deref fix
	d.recordErrorObserved("nil-deref-after-nil-check", "/src/fileA.go")

	// Content does NOT match nil-deref pattern
	content := `package main
func main() {
	fmt.Println("hello")
}
`
	got := d.checkContentAgainstFixed("/src/fileA.go", "/src/fileB.go", content)
	if got != "" {
		t.Errorf("expected no warning for non-matching pattern, got: %s", got)
	}
}

func TestFixAmnesia_MaxWarnings(t *testing.T) {
	d := newFixAmnesiaState()
	d.maxWarnings = 1

	// First trigger
	d.recordErrorObserved("nil-deref-after-nil-check", "/src/fileA.go")
	d.recordErrorObserved("defer-in-loop", "/src/fileC.go")

	content := `
if err != nil {
    return err
}
result.Method()
`
	// First should warn
	got1 := d.checkContentAgainstFixed("/src/fileA.go", "/src/fileB.go", content)
	if got1 == "" {
		t.Error("expected first warning")
	}

	// Second category should not warn because maxWarnings=1
	content2 := `
for i := 0; i < 10; i++ {
	defer f.Close()
}
`
	got2 := d.checkContentAgainstFixed("/src/fileC.go", "/src/fileD.go", content2)
	if got2 != "" {
		t.Errorf("expected no second warning due to maxWarnings, got: %s", got2)
	}
}

func TestFixAmnesia_AlreadyWarnedCategory(t *testing.T) {
	d := newFixAmnesiaState()

	d.recordErrorObserved("nil-deref-after-nil-check", "/src/fileA.go")

	content := `
if err != nil {
    return err
}
result.Method()
`
	// First trigger
	got1 := d.checkContentAgainstFixed("/src/fileA.go", "/src/fileB.go", content)
	if got1 == "" {
		t.Error("expected first warning")
	}

	// Second file with same category should not warn again
	got2 := d.checkContentAgainstFixed("/src/fileA.go", "/src/fileC.go", content)
	if got2 != "" {
		t.Errorf("expected no duplicate warning for same category, got: %s", got2)
	}
}

func TestClassifyToolError(t *testing.T) {
	tests := []struct {
		input   string
		wantCat string
	}{
		{"panic: runtime error: nil pointer dereference", "nil-deref-after-nil-check"},
		{"./main.go:10:2: declared and not used: x", "missing-import"},
		{"./main.go:5:2: imported and not used: \"fmt\"", "missing-import"},
		{"fatal error: concurrent map writes", "map-concurrent-write"},
		{"some random error message", ""},
	}

	for _, tt := range tests {
		cat, _ := classifyToolError("build", tt.input)
		if cat != tt.wantCat {
			t.Errorf("classifyToolError(%q) = %q, want %q", tt.input, cat, tt.wantCat)
		}
	}
}

func TestExtractFilePathFromError(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"./internal/agent/foo.go:10:2: error", "/internal/agent/foo.go"},
		{"no path here", ""},
		{"/absolute/path/to/file.go:5: error", "/absolute/path/to/file.go"},
	}

	for _, tt := range tests {
		got := extractFilePathFromError(tt.input)
		if got != tt.want {
			t.Errorf("extractFilePathFromError(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestAppendIfMissing(t *testing.T) {
	s := []string{"a", "b"}
	s = appendIfMissing(s, "a") // duplicate
	if len(s) != 2 {
		t.Errorf("expected length 2, got %d", len(s))
	}
	s = appendIfMissing(s, "c") // new
	if len(s) != 3 {
		t.Errorf("expected length 3, got %d", len(s))
	}
}
