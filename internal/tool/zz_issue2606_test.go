package tool

import (
	"strings"
	"testing"
)

// #2606: parseReviewDiff consumed the '++' file headers but not the '---'
// side, so every "--- a/<path>" fell into the '-' content branch and
// removedCount over-counted by exactly the number of files; new files
// ("--- /dev/null") reported phantom removals.
func TestIssue2606_ParseReviewDiffHeaderNotCounted(t *testing.T) {
	// Modified file: one real removal -> removedCount must be 1, not 2.
	modified := strings.Join([]string{
		"diff --git a/x.go b/x.go",
		"index 111..222 100644",
		"--- a/x.go",
		"+++ b/x.go",
		"@@ -1,2 +1,2 @@",
		" context",
		"-old line",
		"+new line",
		"", "",
	}, "\n")
	files := parseReviewDiff(modified)
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	if files[0].path != "x.go" {
		t.Fatalf("path = %q, want x.go", files[0].path)
	}
	if files[0].removedCount != 1 {
		t.Fatalf("modified: removedCount = %d, want 1 (header must not count)", files[0].removedCount)
	}
	if files[0].addedCount != 1 {
		t.Fatalf("modified: addedCount = %d, want 1", files[0].addedCount)
	}

	// New file: "--- /dev/null" -> zero removals, not the phantom 1.
	newFile := strings.Join([]string{
		"diff --git a/y.go b/y.go",
		"new file mode 100644",
		"--- /dev/null",
		"+++ b/y.go",
		"@@ -0,0 +1,1 @@",
		"+brand new",
		"", "",
	}, "\n")
	files = parseReviewDiff(newFile)
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	if files[0].removedCount != 0 {
		t.Fatalf("new file: removedCount = %d, want 0 (no phantom removal)", files[0].removedCount)
	}
	if files[0].addedCount != 1 {
		t.Fatalf("new file: addedCount = %d, want 1", files[0].addedCount)
	}

	// Content lines that START with '--' after the header are still real
	// removals (the #1699 case-1 guarantee on the '-' side).
	dashes := strings.Join([]string{
		"diff --git a/z.go b/z.go",
		"--- a/z.go",
		"+++ b/z.go",
		"@@ -1,1 +1,1 @@",
		"--weird removal",
		"", "",
	}, "\n")
	files = parseReviewDiff(dashes)
	if len(files) != 1 || files[0].removedCount != 1 {
		t.Fatalf("'--' content line must still count as removal, got %+v", files)
	}
}
