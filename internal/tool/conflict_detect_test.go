package tool

import (
	"strings"
	"testing"
)

func TestDetectMergeConflicts_none(t *testing.T) {
	content := `package main

func foo() {
	println("hello")
}
`
	regions := DetectMergeConflicts(content)
	if len(regions) != 0 {
		t.Errorf("expected 0 conflict regions, got %d: %+v", len(regions), regions)
	}
}

func TestDetectMergeConflicts_single(t *testing.T) {
	content := `package main

func foo() {
<<<<<<< HEAD
	println("ours")
=======
	println("theirs")
>>>>>>> feature/branch
}
`
	regions := DetectMergeConflicts(content)
	if len(regions) != 1 {
		t.Fatalf("expected 1 conflict region, got %d", len(regions))
	}
	r := regions[0]
	if r.StartLine != 4 {
		t.Errorf("StartLine: expected 4, got %d", r.StartLine)
	}
	if r.MidLine != 6 {
		t.Errorf("MidLine: expected 6, got %d", r.MidLine)
	}
	if r.EndLine != 8 {
		t.Errorf("EndLine: expected 8, got %d", r.EndLine)
	}
	if r.Branch1 != "HEAD" {
		t.Errorf("Branch1: expected 'HEAD', got %q", r.Branch1)
	}
	if r.Branch2 != "feature/branch" {
		t.Errorf("Branch2: expected 'feature/branch', got %q", r.Branch2)
	}
}

func TestDetectMergeConflicts_multiple(t *testing.T) {
	content := `package main

<<<<<<< HEAD
func a() {}
=======
func b() {}
>>>>>>> dev

func main() {
<<<<<<< HEAD
	a()
=======
	b()
>>>>>>> dev
}
`
	regions := DetectMergeConflicts(content)
	if len(regions) != 2 {
		t.Fatalf("expected 2 conflict regions, got %d", len(regions))
	}
	if regions[0].StartLine != 3 || regions[0].EndLine != 7 {
		t.Errorf("region 0: expected start=3 end=7, got start=%d end=%d", regions[0].StartLine, regions[0].EndLine)
	}
	if regions[1].StartLine != 10 || regions[1].EndLine != 14 {
		t.Errorf("region 1: expected start=10 end=14, got start=%d end=%d", regions[1].StartLine, regions[1].EndLine)
	}
}

func TestDetectMergeConflicts_cap(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 10; i++ {
		sb.WriteString("<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>> dev\n\n")
	}
	regions := DetectMergeConflicts(sb.String())
	if len(regions) != maxConflictRegions {
		t.Errorf("expected %d conflict regions (capped), got %d", maxConflictRegions, len(regions))
	}
}

func TestDetectMergeConflicts_unclosed(t *testing.T) {
	content := `package main
<<<<<<< HEAD
println("ours")
`
	regions := DetectMergeConflicts(content)
	if len(regions) != 1 {
		t.Fatalf("expected 1 region (unclosed), got %d", len(regions))
	}
	if regions[0].StartLine != 2 {
		t.Errorf("StartLine: expected 2, got %d", regions[0].StartLine)
	}
	if regions[0].EndLine != 0 {
		t.Errorf("EndLine: expected 0 (unclosed), got %d", regions[0].EndLine)
	}
}

func TestDetectMergeConflicts_falsePositives(t *testing.T) {
	// Strings containing conflict-like patterns should not trigger false positives
	// because the detector checks for markers at the START of the line.
	content := `package main

const msg = ">>>>>>> not a conflict"
const msg2 = "<<<<<<< also not"

func main() {
	x := "======="
}
`
	regions := DetectMergeConflicts(content)
	if len(regions) != 0 {
		t.Errorf("expected 0 false positive regions, got %d", len(regions))
	}
}

func TestDetectMergeConflicts_crlf(t *testing.T) {
	// Windows line endings should not break detection
	content := "package main\r\n\r\n<<<<<<< HEAD\r\nours\r\n=======\r\ntheirs\r\n>>>>>>> dev\r\n"
	regions := DetectMergeConflicts(content)
	if len(regions) != 1 {
		t.Fatalf("expected 1 region with CRLF, got %d", len(regions))
	}
	if regions[0].StartLine != 3 {
		t.Errorf("StartLine: expected 3, got %d", regions[0].StartLine)
	}
}

func TestFormatConflictWarning_empty(t *testing.T) {
	if FormatConflictWarning(nil) != "" {
		t.Error("expected empty string for nil regions")
	}
	if FormatConflictWarning([]ConflictRegion{}) != "" {
		t.Error("expected empty string for empty regions")
	}
}

func TestFormatConflictWarning_single(t *testing.T) {
	regions := []ConflictRegion{
		{StartLine: 5, MidLine: 7, EndLine: 9, Branch1: "HEAD", Branch2: "feature/x"},
	}
	warning := FormatConflictWarning(regions)
	if !strings.Contains(warning, "an unresolved merge conflict") {
		t.Errorf("warning should mention single conflict: %q", warning)
	}
	if !strings.Contains(warning, "lines 5-9") {
		t.Errorf("warning should mention line range 5-9: %q", warning)
	}
	if !strings.Contains(warning, "HEAD") {
		t.Errorf("warning should mention branch1: %q", warning)
	}
	if !strings.Contains(warning, "feature/x") {
		t.Errorf("warning should mention branch2: %q", warning)
	}
}

func TestFormatConflictWarning_multiple(t *testing.T) {
	regions := []ConflictRegion{
		{StartLine: 3, EndLine: 7, Branch1: "HEAD", Branch2: "dev"},
		{StartLine: 10, EndLine: 14, Branch1: "HEAD", Branch2: "dev"},
	}
	warning := FormatConflictWarning(regions)
	if !strings.Contains(warning, "2 unresolved merge conflicts") {
		t.Errorf("warning should mention multiple conflicts: %q", warning)
	}
}

func TestCheckContentForConflicts_clean(t *testing.T) {
	content := "package main\nfunc main() {}\n"
	if CheckContentForConflicts(content) != "" {
		t.Error("expected empty warning for clean content")
	}
}

func TestCheckContentForConflicts_withConflicts(t *testing.T) {
	content := "package main\n<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>> dev\n"
	warning := CheckContentForConflicts(content)
	if warning == "" {
		t.Error("expected non-empty warning for conflicted content")
	}
	if !strings.Contains(warning, "[WARNING]") {
		t.Errorf("warning should contain [WARNING] tag: %q", warning)
	}
}
