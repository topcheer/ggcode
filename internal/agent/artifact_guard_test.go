package agent

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Merge conflict marker detection tests
// ---------------------------------------------------------------------------

func TestCheckMergeConflictMarkers_Detected(t *testing.T) {
	content := `package main

<<<<<<< HEAD
func old() {}
=======
func new() {}
>>>>>>> feature-branch

func main() {}
`
	result := checkMergeConflictMarkers("test.go", content)
	if result == "" {
		t.Fatal("expected merge conflict marker warning, got empty")
	}
	if !strings.Contains(result, "merge conflict") {
		t.Errorf("warning should mention 'merge conflict', got: %s", result)
	}
	if !strings.Contains(result, "3") {
		t.Errorf("warning should mention 3 markers, got: %s", result)
	}
}

func TestCheckMergeConflictMarkers_NoMarkers(t *testing.T) {
	content := `package main

func main() {
	if a < b && c > d {
		return
	}
}
`
	// Ensure comparison operators that resemble < and > don't trigger.
	result := checkMergeConflictMarkers("test.go", content)
	if result != "" {
		t.Errorf("expected no warning for clean code, got: %s", result)
	}
}

func TestCheckMergeConflictMarkers_OnlySeparator(t *testing.T) {
	content := `package main

func main() {}
=======
func extra() {}
`
	result := checkMergeConflictMarkers("test.go", content)
	if result == "" {
		t.Fatal("expected warning for separator-only conflict marker")
	}
}

func TestCheckMergeConflictMarkers_IndentedMarker(t *testing.T) {
	content := `func main() {
	<<<<<<< HEAD
	x = 1
	=======
	x = 2
	>>>>>>> dev
}
`
	result := checkMergeConflictMarkers("test.go", content)
	if result == "" {
		t.Fatal("expected warning for indented merge markers")
	}
}

func TestCheckMergeConflictMarkers_EmptyContent(t *testing.T) {
	result := checkMergeConflictMarkers("test.go", "   \n  ")
	if result != "" {
		t.Errorf("expected empty result for whitespace-only content, got: %s", result)
	}
}

func TestCheckMergeConflictMarkers_NoFalsePositiveBase64(t *testing.T) {
	// A line of 7+ equals could appear in base64 strings. Make sure "======="
	// inside a base64 blob doesn't trigger — only exact line match counts.
	content := "data := \"ABCD======\"\n"
	result := checkMergeConflictMarkers("test.go", content)
	if result != "" {
		t.Errorf("expected no false positive for base64 padding, got: %s", result)
	}
}

func TestCheckMergeConflictMarkers_JavaScript(t *testing.T) {
	content := `function hello() {
<<<<<<< HEAD
  console.log("hello");
=======
  console.log("world");
>>>>>>> main
}
`
	result := checkMergeConflictMarkers("app.js", content)
	if result == "" {
		t.Fatal("expected warning for JS merge conflict markers")
	}
}

// ---------------------------------------------------------------------------
// Content growth detection tests
// ---------------------------------------------------------------------------

func TestCheckContentGrowth_MassiveGrowth(t *testing.T) {
	// Create old content with ~10 lines, new content with ~60 lines (6x).
	old := "package main\n\nfunc a() {}\nfunc b() {}\nfunc c() {}\nfunc d() {}\nfunc e() {}\nfunc f() {}\nfunc g() {}\nfunc h() {}\nfunc i() {}\n"
	new := old
	for i := 0; i < 50; i++ {
		new += "func extra() {}\n"
	}
	result := checkContentGrowth("test.go", old, new)
	if result == "" {
		t.Fatal("expected growth warning for 6x file growth")
	}
	if !strings.Contains(result, "duplication") {
		t.Errorf("warning should mention duplication, got: %s", result)
	}
}

func TestCheckContentGrowth_NoGrowth(t *testing.T) {
	old := strings.Repeat("line\n", 20)
	new := old + "added line\n"
	result := checkContentGrowth("test.go", old, new)
	if result != "" {
		t.Errorf("expected no warning for small growth, got: %s", result)
	}
}

func TestCheckContentGrowth_SmallFileSkipped(t *testing.T) {
	old := "a\nb\n"
	new := strings.Repeat("x\n", 50)
	result := checkContentGrowth("test.go", old, new)
	if result != "" {
		t.Errorf("expected no warning for small file (<10 lines), got: %s", result)
	}
}

func TestCheckContentGrowth_NewFileSkipped(t *testing.T) {
	new := strings.Repeat("line\n", 100)
	result := checkContentGrowth("test.go", "", new)
	if result != "" {
		t.Errorf("expected no warning for new file, got: %s", result)
	}
}

func TestCheckContentGrowth_EmptyNewContent(t *testing.T) {
	old := strings.Repeat("line\n", 20)
	result := checkContentGrowth("test.go", old, "")
	if result != "" {
		t.Errorf("expected no warning for empty new content, got: %s", result)
	}
}

func TestCheckContentGrowth_Exactly5x(t *testing.T) {
	old := strings.Repeat("line\n", 10) // 10 lines
	new := strings.Repeat("line\n", 50) // 50 lines = 5.0x
	result := checkContentGrowth("test.go", old, new)
	if result == "" {
		t.Fatal("expected warning at exactly 5x growth")
	}
}

func TestCheckContentGrowth_JustUnder5x(t *testing.T) {
	old := strings.Repeat("line\n", 10) // 10 lines
	new := strings.Repeat("line\n", 49) // 49 lines = 4.9x
	result := checkContentGrowth("test.go", old, new)
	if result != "" {
		t.Errorf("expected no warning at 4.9x growth, got: %s", result)
	}
}

// ---------------------------------------------------------------------------
// Integration: write_integrity pipeline includes the new checks
// ---------------------------------------------------------------------------

func TestCheckWriteIntegrity_MergeConflictMarkers(t *testing.T) {
	newContent := `package main

<<<<<<< HEAD
func a() {}
=======
func b() {}
>>>>>>> dev
`
	result := checkWriteIntegrity("test.go", "", newContent)
	if !strings.Contains(result, "merge conflict") {
		t.Errorf("write integrity should detect merge conflict markers, got: %s", result)
	}
}

func TestCheckWriteIntegrity_ContentGrowth(t *testing.T) {
	old := strings.Repeat("func thing() {}\n", 10)
	new := old + strings.Repeat("func dup() {}\n", 100)
	result := checkWriteIntegrity("test.go", old, new)
	if !strings.Contains(result, "duplication") {
		t.Errorf("write integrity should detect massive growth, got: %s", result)
	}
}

func TestCheckWriteIntegrity_CleanFile(t *testing.T) {
	old := "package main\n\nfunc a() {}\n"
	new := "package main\n\nfunc a() {}\nfunc b() {}\n"
	result := checkWriteIntegrity("test.go", old, new)
	if result != "" {
		t.Errorf("expected no warnings for clean edit, got: %s", result)
	}
}
