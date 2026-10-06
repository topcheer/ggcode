package memory

// Regression probes for #3053:
//   - C1: the health-report age range prints oldest-newest; the old
//     argument order reversed the semantics ("3-45" read as oldest=3 days).
//   - C2: a legal same-level basename like "..foo.md" must count as INSIDE
//     the working directory (only a true parent escape is outside).

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestIssue3053_C1_AgeRangeOrdering(t *testing.T) {
	r := HealthReport{OldestDays: 45, NewestDays: 3}
	s := r.FormatHealthReport()
	m := regexp.MustCompile(`Age range: (\d+)-(\d+) days \(oldest-newest\)`).FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("age-range line missing from report:\n%s", s)
	}
	if m[1] != "45" || m[2] != "3" {
		t.Fatalf("#3053-C1: age range must print oldest(45)-newest(3), got %s-%s", m[1], m[2])
	}
}

func TestIssue3053_C2_DotDotBasenameIsInside(t *testing.T) {
	wd := t.TempDir()
	odd := filepath.Join(wd, "..foo.md")
	if err := os.WriteFile(odd, []byte("hint content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if isOutsideWorkingDir(odd, wd) {
		t.Fatal("#3053-C2: legal '..foo.md' basename misclassified as outside the working dir")
	}
	// A real parent escape stays outside.
	if !isOutsideWorkingDir(filepath.Join(wd, "..", "escape.md"), wd) {
		t.Fatal("parent escape must stay outside")
	}
	// And a nested sibling under a ".."-prefixed DIRECTORY stays inside.
	nested := filepath.Join(wd, "..weird", "note.md")
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatal(err)
	}
	if isOutsideWorkingDir(nested, wd) {
		t.Fatal("path under a legal '..weird' directory must stay inside")
	}
}

var _ = strings.TrimSpace // reserved for future probe extensions
