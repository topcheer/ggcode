package tool

import "testing"

// #2233 probe: a pure R100 rename diff must register the file.
func TestParseDiffStatsPureRename(t *testing.T) {
	diff := `diff --git a/old/path.go b/new/path.go
similarity index 100%
rename from old/path.go
rename to new/path.go
`
	s := parseDiffStats(diff)
	if len(s.files) != 1 || s.files[0] != "new/path.go" {
		t.Fatalf("#2233: pure rename not registered: %v", s.files)
	}
	if s.additions != 0 || s.deletions != 0 {
		t.Fatalf("rename must not count as +/- lines: +%d -%d", s.additions, s.deletions)
	}
}

// Mixed: a rename plus a normal edit - both land in the set.
func TestParseDiffStatsRenamePlusEdit(t *testing.T) {
	diff := `diff --git a/old.go b/new.go
similarity index 100%
rename from old.go
rename to new.go
diff --git a/keep.go b/keep.go
index 111..222 100644
--- a/keep.go
+++ b/keep.go
@@ -1 +1 @@
-a
+b
`
	s := parseDiffStats(diff)
	if len(s.files) != 2 {
		t.Fatalf("expected new.go + keep.go, got %v", s.files)
	}
	found := map[string]bool{}
	for _, f := range s.files {
		found[f] = true
	}
	if !found["new.go"] || !found["keep.go"] {
		t.Fatalf("missing expected files: %v", s.files)
	}
	if s.additions != 1 || s.deletions != 1 {
		t.Fatalf("edit counts wrong: +%d -%d", s.additions, s.deletions)
	}
}
