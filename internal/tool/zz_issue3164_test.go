package tool

// Regression probes for #3164: content lines whose source starts with
// "++" (rendered "+++" in the diff) or "--"/"---" (rendered "---"/more)
// were misread as file headers and skipped by the +/- counters - the
// Additions/Deletions heuristics undercounted. Real headers ("+++ b/x",
// "+++ /dev/null", "--- a/x") are consumed by the earlier regex
// branches; a "+++" line is a residual header only when IMMEDIATELY
// after a consumed minus-side header, and "--- /dev/null" is the only
// literal minus-side header left.

import (
	"testing"
)

func findChange3164(files []FileChangeInfo, path string) *FileChangeInfo {
	for i := range files {
		if files[i].Path == path {
			return &files[i]
		}
	}
	return nil
}

// Content lines starting with "++"/"--"/"----" in a normal (a/..b/)
// modified-file diff must be counted.
func TestIssue3164_HeaderShapedContentCounted(t *testing.T) {
	diff := `diff --git a/docs/guide.md b/docs/guide.md
index 111..222 100644
--- a/docs/guide.md
+++ b/docs/guide.md
@@ -1,4 +1,6 @@
 context line
-plain old
+plain new
---- markdown hr (deleted)
++++ markdown hr (added)
--- strikethrough note (deleted)
+++ nested diff example (added)
-- dash marker (deleted)
++ plus marker (added)
`
	files := parseFileChanges(diff)
	fc := findChange3164(files, "docs/guide.md")
	if fc == nil {
		t.Fatalf("file missing from parsed changes: %+v", files)
	}
	// adds: plain new, ++++ hr, +++ nested, ++ marker = 4
	// dels: plain old, ---- hr, --- note, -- marker = 4
	if fc.Additions != 4 {
		t.Errorf("Additions = %d, want 4 (header-shaped content counted, #3164)", fc.Additions)
	}
	if fc.Deletions != 4 {
		t.Errorf("Deletions = %d, want 4 (header-shaped content counted, #3164)", fc.Deletions)
	}
}

// New-file diffs: "--- /dev/null" followed by "+++ b/x" must stay a
// header pair (0 phantom adds), while header-shaped CONTENT inside the
// hunk still counts.
func TestIssue3164_NewFileHeaderPairStillSkipped(t *testing.T) {
	diff := `diff --git a/new.md b/new.md
new file mode 100644
index 000..333
--- /dev/null
+++ b/new.md
@@ -0,0 +1,2 @@
+first real line
+++ literally-header-shaped content line
`
	files := parseFileChanges(diff)
	fc := findChange3164(files, "new.md")
	if fc == nil {
		t.Fatalf("new.md missing from parsed changes: %+v", files)
	}
	// "+++ b/new.md" is a header (adjacent to "--- /dev/null"), NOT an add.
	// adds: first real line + the header-shaped content = 2.
	if fc.Additions != 2 {
		t.Errorf("Additions = %d, want 2 (header pair skipped, content counted)", fc.Additions)
	}
	if fc.Deletions != 0 {
		t.Errorf("Deletions = %d, want 0", fc.Deletions)
	}
}

// Deleted-file diffs: "--- a/x" + "+++ /dev/null" keep 0/0 semantics.
func TestIssue3164_DeletedFileStillZero(t *testing.T) {
	diff := `diff --git a/gone.go b/gone.go
deleted file mode 100644
index 444..000
--- a/gone.go
+++ /dev/null
@@ -1,2 +0,0 @@
-func Gone() {}
---- weird but deleted content line
`
	files := parseFileChanges(diff)
	fc := findChange3164(files, "gone.go")
	if fc == nil {
		t.Fatalf("gone.go missing from parsed changes: %+v", files)
	}
	if fc.Additions != 0 || fc.Deletions != 2 {
		t.Errorf("deleted file counts = +%d/-%d, want +0/-2 (header pair skipped, content counted)", fc.Additions, fc.Deletions)
	}
}
