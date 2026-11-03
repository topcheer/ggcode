package agent

import (
	"strings"
	"testing"
)

func TestFixAmnesia_BasicDetection(t *testing.T) {
	d := newFixAmnesiaState()

	d.recordErrorObserved("defer-in-loop", "/src/fileA.go")
	d.recordFileEdited("/src/fileA.go")

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

	d.recordErrorObserved("defer-in-loop", "/src/fileA.go")
	d.recordFileEdited("/src/fileA.go")
	content := `
for i := 0; i < 10; i++ {
	defer f.Close()
}
`

	got := d.checkContentAgainstFixed("/src/fileA.go", "/src/fileA.go", content)
	if got != "" {
		t.Errorf("expected no warning for same file, got: %s", got)
	}
}

func TestFixAmnesia_NoPriorFixNoWarning(t *testing.T) {
	d := newFixAmnesiaState()

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

	d.recordErrorObserved("defer-in-loop", "/src/fileA.go")
	d.recordFileEdited("/src/fileA.go")

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

	d.recordErrorObserved("defer-in-loop", "/src/fileA.go")
	d.recordFileEdited("/src/fileA.go")

	d.recordErrorObserved("map-concurrent-write", "/src/fileC.go")
	d.recordFileEdited("/src/fileC.go")
	content := `
for i := 0; i < 10; i++ {
	defer f.Close()
}
`

	got1 := d.checkContentAgainstFixed("/src/fileA.go", "/src/fileB.go", content)
	if got1 == "" {
		t.Error("expected first warning")
	}

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
	d.recordFileEdited("/src/fileA.go")
	content := `
for i := 0; i < 10; i++ {
	defer f.Close()
}
`

	got1 := d.checkContentAgainstFixed("/src/fileA.go", "/src/fileB.go", content)
	if got1 == "" {
		t.Error("expected first warning")
	}

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
	s = appendIfMissing(s, "a")
	if len(s) != 2 {
		t.Errorf("expected length 2, got %d", len(s))
	}
	s = appendIfMissing(s, "c")
	if len(s) != 3 {
		t.Errorf("expected length 3, got %d", len(s))
	}
}

func TestFixAmnesiaRelativeVsAbsolutePromotion(t *testing.T) {
	d := newFixAmnesiaState()
	d.recordErrorObserved("missing-import", "internal/agent/foo.go")

	d.recordFileEdited("/repo/root/internal/agent/foo.go")
	if len(d.fixedPatterns["missing-import"]) != 1 {
		t.Fatalf("relative-observed vs absolute-edited promotion failed: %v", d.fixedPatterns["missing-import"])
	}

	d2 := newFixAmnesiaState()
	d2.recordErrorObserved("missing-import", "a/b.go")
	d2.recordFileEdited("a/b.go")
	if len(d2.fixedPatterns["missing-import"]) != 1 {
		t.Fatal("same-form promotion regressed")
	}

	d3 := newFixAmnesiaState()
	d3.recordErrorObserved("missing-import", "internal/agent/foo.go")
	d3.recordFileEdited(`C:\repo\root\internal\agent\foo.go`)
	if len(d3.fixedPatterns["missing-import"]) != 1 {
		t.Fatalf("backslash-edited vs slash-observed promotion failed: %v", d3.fixedPatterns["missing-import"])
	}
}

func TestFixAmnesiaImportInDiffPrefixedHunk(t *testing.T) {
	if missingImportInContent("+ import \"fmt\"\n+ fmt.Println(\"hi\")\n") {
		t.Fatal("import present in the same diff hunk still reported missing")
	}
	if !missingImportInContent("+ fmt.Println(\"hi\")\n") {
		t.Fatal("missing import no longer detected")
	}
}

func TestFixAmnesiaSameFileAnchoring(t *testing.T) {
	ok := [][2]string{
		{"./internal/agent/foo.go", "/Volumes/w/ggcode/internal/agent/foo.go"},
		{"/w/internal/agent/foo.go", "internal/agent/foo.go"},
	}
	for _, c := range ok {
		if !fixAmnesiaSameFile(c[0], c[1]) {
			t.Errorf("%q vs %q must match", c[0], c[1])
		}
	}
	bad := [][2]string{
		{"foo_test.go", "/w/other/foo_test.go"},
		{"foo.go", "/w/anywhere/foo.go"},
		{"agent/foo.go", "/w/desktop/wailskit/agent/foo.go"},
		{"/w/a/internal/x/foo.go", "/w/b/internal/x/foo.go"},
	}
	for _, c := range bad {
		if fixAmnesiaSameFile(c[0], c[1]) {
			t.Errorf("%q vs %q must NOT match (anchoring)", c[0], c[1])
		}
	}
}
