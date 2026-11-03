package tool

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

	if fc.Additions != 4 {
		t.Errorf("Additions = %d, want 4 (header-shaped content counted, #3164)", fc.Additions)
	}
	if fc.Deletions != 4 {
		t.Errorf("Deletions = %d, want 4 (header-shaped content counted, #3164)", fc.Deletions)
	}
}

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

	if fc.Additions != 2 {
		t.Errorf("Additions = %d, want 2 (header pair skipped, content counted)", fc.Additions)
	}
	if fc.Deletions != 0 {
		t.Errorf("Deletions = %d, want 0", fc.Deletions)
	}
}

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
