package tool

import (
	"strings"
	"testing"
)

// --- parseFileChanges ---

func TestParseFileChanges_Basic(t *testing.T) {
	diff := `diff --git a/internal/agent/loop.go b/internal/agent/loop.go
--- a/internal/agent/loop.go
+++ b/internal/agent/loop.go
@@ -1,3 +1,5 @@
 existing
+new line 1
+new line 2
-old line
diff --git a/README.md b/README.md
--- a/README.md
+++ b/README.md
@@ -1,1 +1,2 @@
 docs
+new docs
`
	files := parseFileChanges(diff)
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if files[0].Path != "internal/agent/loop.go" {
		t.Errorf("file[0] path = %q, want internal/agent/loop.go", files[0].Path)
	}
	if files[0].Category != CatSource {
		t.Errorf("file[0] category = %v, want CatSource", files[0].Category)
	}
	if files[0].Dir != "internal/agent" {
		t.Errorf("file[0] dir = %q, want internal/agent", files[0].Dir)
	}
	if files[0].Additions != 2 {
		t.Errorf("file[0] additions = %d, want 2", files[0].Additions)
	}
	if files[0].Deletions != 1 {
		t.Errorf("file[0] deletions = %d, want 1", files[0].Deletions)
	}
	if files[1].Category != CatDocs {
		t.Errorf("file[1] category = %v, want CatDocs", files[1].Category)
	}
}

func TestParseFileChanges_Empty(t *testing.T) {
	files := parseFileChanges("")
	if len(files) != 0 {
		t.Errorf("expected 0 files for empty diff, got %d", len(files))
	}
}

// --- testToSourceBase ---

func TestTestToSourceBase(t *testing.T) {
	cases := []struct {
		testBase string
		want     string
	}{
		{"handler_test.go", "handler.go"},
		{"auth.test.ts", "auth.ts"},
		{"auth.spec.js", "auth.js"},
		{"test_user.py", "user.py"},
		{"user_test.py", "user.py"},
		{"user_spec.rb", "user.rb"},
		{"math_test.rs", "math.rs"},
		{"notatest.go", ""}, // not a test file (no _test suffix)
		{"random.txt", ""},  // unknown extension
		{"config.yaml", ""}, // config, not test
	}
	for _, tc := range cases {
		got := testToSourceBase(tc.testBase)
		if got != tc.want {
			t.Errorf("testToSourceBase(%q) = %q, want %q", tc.testBase, got, tc.want)
		}
	}
}

// --- SuggestCommitPartition ---

func TestSuggestCommitPartition_CohesiveReturnsNil(t *testing.T) {
	// Single source file + its test should be one group — no partition needed.
	diff := `diff --git a/handler.go b/handler.go
--- a/handler.go
+++ b/handler.go
@@ -1,1 +1,2 @@
 code
+new code
diff --git a/handler_test.go b/handler_test.go
--- a/handler_test.go
+++ b/handler_test.go
@@ -1,1 +1,2 @@
 code
+new test
`
	groups := SuggestCommitPartition(diff)
	if groups != nil {
		t.Errorf("expected nil for cohesive source+test, got %d groups", len(groups))
	}
}

func TestSuggestCommitPartition_MultiConcern(t *testing.T) {
	diff := `diff --git a/internal/agent/loop.go b/internal/agent/loop.go
--- a/internal/agent/loop.go
+++ b/internal/agent/loop.go
@@ -1,1 +1,3 @@
 code
+feat1
+feat2
diff --git a/README.md b/README.md
--- a/README.md
+++ b/README.md
@@ -1,1 +1,2 @@
 docs
+new docs
diff --git a/config.yaml b/config.yaml
--- a/config.yaml
+++ b/config.yaml
@@ -1,1 +1,2 @@
 mod
+new dep
`
	groups := SuggestCommitPartition(diff)
	if len(groups) < 2 {
		t.Fatalf("expected at least 2 groups, got %d", len(groups))
	}
	// Source group should be first (most lines).
	if groups[0].Category != CatSource {
		t.Errorf("expected first group to be source, got %v", groups[0].Category)
	}
	// Should have source, docs, and config groups.
	cats := make(map[FileCategory]bool)
	for _, g := range groups {
		cats[g.Category] = true
	}
	if !cats[CatSource] || !cats[CatDocs] || !cats[CatConfig] {
		t.Errorf("expected source+docs+config groups, got: %v", cats)
	}
}

func TestSuggestCommitPartition_TestMergesIntoSource(t *testing.T) {
	diff := `diff --git a/internal/agent/loop.go b/internal/agent/loop.go
--- a/internal/agent/loop.go
+++ b/internal/agent/loop.go
@@ -1,1 +1,2 @@
 code
+new code
diff --git a/internal/agent/loop_test.go b/internal/agent/loop_test.go
--- a/internal/agent/loop_test.go
+++ b/internal/agent/loop_test.go
@@ -1,1 +1,2 @@
 code
+new test
diff --git a/README.md b/README.md
--- a/README.md
+++ b/README.md
@@ -1,1 +1,2 @@
 docs
+new docs
`
	groups := SuggestCommitPartition(diff)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups (source+test merged, docs separate), got %d", len(groups))
	}
	// Find the source group and verify it includes the test file.
	var sourceGroup *CommitGroup
	for i := range groups {
		if groups[i].Category == CatSource {
			sourceGroup = &groups[i]
			break
		}
	}
	if sourceGroup == nil {
		t.Fatal("expected a source group")
	}
	if len(sourceGroup.Files) != 2 {
		t.Errorf("expected source group to include merged test file (2 files), got %d", len(sourceGroup.Files))
	}
}

func TestSuggestCommitPartition_DirectorySeparation(t *testing.T) {
	// Two source files in different directories should be separate groups.
	diff := `diff --git a/internal/agent/loop.go b/internal/agent/loop.go
--- a/internal/agent/loop.go
+++ b/internal/agent/loop.go
@@ -1,1 +1,2 @@
 code
+new code
diff --git a/internal/tool/util.go b/internal/tool/util.go
--- a/internal/tool/util.go
+++ b/internal/tool/util.go
@@ -1,1 +1,2 @@
 code
+new code
`
	groups := SuggestCommitPartition(diff)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups for different directories, got %d", len(groups))
	}
	if groups[0].Dir == groups[1].Dir {
		t.Errorf("expected different directories, both = %q", groups[0].Dir)
	}
}

func TestSuggestCommitPartition_SortedBySize(t *testing.T) {
	// Two source files in DIFFERENT directories so they form separate groups.
	diff := `diff --git a/pkg/big.go b/pkg/big.go
--- a/pkg/big.go
+++ b/pkg/big.go
@@ -1,1 +1,6 @@
 code
+l1
+l2
+l3
+l4
 diff --git a/lib/small.go b/lib/small.go
--- a/lib/small.go
+++ b/lib/small.go
@@ -1,1 +1,2 @@
 code
+l1
`
	groups := SuggestCommitPartition(diff)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if groups[0].TotalLines() < groups[1].TotalLines() {
		t.Errorf("expected groups sorted by size descending: %d before %d",
			groups[0].TotalLines(), groups[1].TotalLines())
	}
}

// --- FormatCommitPartition ---

func TestFormatCommitPartition_Empty(t *testing.T) {
	if s := FormatCommitPartition(nil); s != "" {
		t.Errorf("expected empty string for nil groups, got %q", s)
	}
}

func TestFormatCommitPartition_Format(t *testing.T) {
	diff := `diff --git a/handler.go b/handler.go
--- a/handler.go
+++ b/handler.go
@@ -1,1 +1,3 @@
 code
+feat1
+feat2
diff --git a/README.md b/README.md
--- a/README.md
+++ b/README.md
@@ -1,1 +1,2 @@
 docs
+new docs
`
	groups := SuggestCommitPartition(diff)
	s := FormatCommitPartition(groups)
	if !strings.Contains(s, "[Commit partition]") {
		t.Errorf("expected header, got: %s", s)
	}
	if !strings.Contains(s, "2 focused commits") {
		t.Errorf("expected mention of 2 commits: %s", s)
	}
	if !strings.Contains(s, "handler.go") {
		t.Errorf("expected handler.go in output: %s", s)
	}
	if !strings.Contains(s, "README.md") {
		t.Errorf("expected README.md in output: %s", s)
	}
	if !strings.Contains(s, "git_add") {
		t.Errorf("expected git_add instruction: %s", s)
	}
}

// --- Integration with AnalyzeCommitScope via git_commit ---

func TestSuggestCommitPartition_LargeGroupTruncation(t *testing.T) {
	// 7 source files in one dir + 1 doc in another → group has >5 files.
	var diff string
	for i := 0; i < 7; i++ {
		diff += "diff --git a/pkg/file" + string(rune('a'+i)) + ".go b/pkg/file" + string(rune('a'+i)) + ".go\n"
		diff += "+++ b/pkg/file" + string(rune('a'+i)) + ".go\n"
		diff += "@@ -1,1 +1,2 @@\n old\n+change\n"
	}
	diff += "diff --git a/README.md b/README.md\n+++ b/README.md\n@@ -1,1 +1,2 @@\n old\n+change\n"

	groups := SuggestCommitPartition(diff)
	s := FormatCommitPartition(groups)
	if !strings.Contains(s, "... and 2 more") {
		t.Errorf("expected truncation indicator for >5 files in group: %s", s)
	}
}
