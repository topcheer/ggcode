package agent

import (
	"strings"
	"testing"
)

func TestFixAmnesia_BasicDetection(t *testing.T) {
	d := newFixAmnesiaState()

	// Issue #1059: using defer-in-loop instead of removed nil-deref-after-nil-check
	d.recordErrorObserved("defer-in-loop", "/src/fileA.go")
	d.recordFileEdited("/src/fileA.go") // #754: observed errors only arm after a successful edit
	// Now check content in fileB.go that matches the pattern
	content := `
for i := 0; i < 10; i++ {
	defer f.Close()
}
`
	got := d.checkContentAgainstFixed("/src/fileA.go", "/src/fileB.go", content)
	if got == "" {
		t.Error("expected fix amnesia warning, got empty")
	}
	if !strings.Contains(got, "defer") {
		t.Errorf("expected 'defer' in warning, got: %s", got)
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
	// Issue #1059: using defer-in-loop instead of removed nil-deref-after-nil-check
	d.recordErrorObserved("defer-in-loop", "/src/fileA.go")
	d.recordFileEdited("/src/fileA.go") // #754: observed errors only arm after a successful edit
	content := `
for i := 0; i < 10; i++ {
	defer f.Close()
}
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
for i := 0; i < 10; i++ {
	defer f.Close()
}
`
	got := d.checkContentAgainstFixed("", "/src/fileB.go", content)
	if got != "" {
		t.Errorf("expected no warning without prior fix, got: %s", got)
	}
}

func TestFixAmnesia_DifferentCategoryNoWarning(t *testing.T) {
	d := newFixAmnesiaState()
	// Issue #1059: using defer-in-loop instead of removed nil-deref-after-nil-check
	d.recordErrorObserved("defer-in-loop", "/src/fileA.go")
	d.recordFileEdited("/src/fileA.go") // #754: observed errors only arm after a successful edit
	// Content does NOT match defer-in-loop pattern
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

	// First trigger (Issue #1059: defer-in-loop replaced removed nil-deref category)
	d.recordErrorObserved("defer-in-loop", "/src/fileA.go")
	d.recordFileEdited("/src/fileA.go") // #754: observed errors only arm after a successful edit
	// Second category armed in a different file
	d.recordErrorObserved("map-concurrent-write", "/src/fileC.go")
	d.recordFileEdited("/src/fileC.go") // #754: observed errors only arm after a successful edit
	content := `
for i := 0; i < 10; i++ {
	defer f.Close()
}
`
	// First should warn
	got1 := d.checkContentAgainstFixed("/src/fileA.go", "/src/fileB.go", content)
	if got1 == "" {
		t.Error("expected first warning")
	}

	// Second category should not warn because maxWarnings=1
	content2 := `
go func() {
	m[key] = 1
}
`
	got2 := d.checkContentAgainstFixed("/src/fileC.go", "/src/fileD.go", content2)
	if got2 != "" {
		t.Errorf("expected no second warning due to maxWarnings, got: %s", got2)
	}
}

func TestFixAmnesia_AlreadyWarnedCategory(t *testing.T) {
	d := newFixAmnesiaState()

	d.recordErrorObserved("defer-in-loop", "/src/fileA.go")
	d.recordFileEdited("/src/fileA.go") // #754: observed errors only arm after a successful edit
	content := `
for i := 0; i < 10; i++ {
	defer f.Close()
}
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
		// #754: these are NOT missing-import errors -- "declared and not
		// used" is an unused variable, "imported and not used" is fixed by
		// DELETING an import. Old rows asserted the misclassification.
		{"./main.go:10:2: declared and not used: x", "unused-variable"},
		{"./main.go:5:2: imported and not used: \"fmt\"", "unused-import"},
		{"./main.go:7:2: undefined: fmt.Stringer", "missing-import"},
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
