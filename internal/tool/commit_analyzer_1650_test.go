package tool

import (
	"slices"
	"testing"
)

// #1650 case 1 probes: parseDiffStats must register binary changes, pure
// deletions (+++ /dev/null), and pure mode changes - previously all three
// shapes produced zero files, so the maxFilesPerCommit/maxLinesPerCommit
// warnings silently missed large pure-deletion changes.

func TestParseDiffStatsNormalAndDeletion(t *testing.T) {
	diff := `diff --git a/keep.txt b/keep.txt
index 111..222 100644
--- a/keep.txt
+++ b/keep.txt
@@ -1 +1 @@
-old
+new
diff --git a/gone.txt b/gone.txt
index 333..000 100644
--- a/gone.txt
+++ /dev/null
@@ -1 +0,0 @@
-all gone
`
	s := parseDiffStats(diff)
	if len(s.files) != 2 {
		t.Fatalf("expected keep.txt + gone.txt, got %v", s.files)
	}
	if !slices.Contains(s.files, "gone.txt") {
		t.Fatalf("#1650: deleted file gone.txt missing (dev/null path): %v", s.files)
	}
	if s.additions != 1 || s.deletions != 2 {
		t.Fatalf("counts: +%d -%d (want +1 -2)", s.additions, s.deletions)
	}
}

func TestParseDiffStatsBinaryChange(t *testing.T) {
	diff := `diff --git a/app.bin b/app.bin
index 111..222 100644
Binary files a/app.bin and b/app.bin differ
`
	s := parseDiffStats(diff)
	if len(s.files) != 1 || s.files[0] != "app.bin" {
		t.Fatalf("#1650: binary change not registered: %v", s.files)
	}
}

func TestParseDiffStatsPureModeChange(t *testing.T) {
	diff := `diff --git a/script.sh b/script.sh
old mode 100644
new mode 100755
`
	s := parseDiffStats(diff)
	if len(s.files) != 1 || s.files[0] != "script.sh" {
		t.Fatalf("#1650: pure mode change not registered: %v", s.files)
	}
}
