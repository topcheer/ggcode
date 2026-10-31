package tool

import "testing"

// #3956: parseDiffStats must count header-shaped content lines (+++/---
// prefixed diff content: C++ increments, markdown hr, nested patch tails)
// instead of unconditionally skipping them, matching the #3164
// header-adjacency semantics already applied to parseFileChanges.
func TestParseDiffStatsCountsHeaderShapedContent(t *testing.T) {
	diff := "diff --git a/count.go b/count.go\n" +
		"index 111..222 100644\n" +
		"--- a/count.go\n" +
		"+++ b/count.go\n" +
		"@@ -1,3 +1,4 @@\n" +
		" context\n" +
		"+++count;\n" + // added line whose content is "++count;"
		"+plain\n" +
		"---count;\n" + // deleted line whose content is "--count;"
		"-----\n" // deleted markdown hr "----"

	stats := parseDiffStats(diff)
	if stats.additions != 2 {
		t.Errorf("additions = %d, want 2 (+++count; and +plain are content; old code skipped +++count;)", stats.additions)
	}
	if stats.deletions != 2 {
		t.Errorf("deletions = %d, want 2 (---count; and ----- are content; old code skipped both)", stats.deletions)
	}
	if len(stats.files) != 1 || stats.files[0] != "count.go" {
		t.Errorf("files = %v, want [count.go]", stats.files)
	}
}

// #3956 regression guard: real file headers (--- a/x, +++ b/x, new-file
// --- /dev/null, deleted-file +++ /dev/null) must stay uncounted and the
// file set must stay correct after the adjacency port.
func TestParseDiffStatsStillSkipsRealHeaders(t *testing.T) {
	diff := "diff --git a/new.go b/new.go\n" +
		"new file mode 100644\n" +
		"index 000..111 100644\n" +
		"--- /dev/null\n" +
		"+++ b/new.go\n" +
		"@@ -0,0 +1,2 @@\n" +
		"+hello\n" +
		"+world\n" +
		"diff --git a/gone.go b/gone.go\n" +
		"deleted file mode 100644\n" +
		"index 111..000 100644\n" +
		"--- a/gone.go\n" +
		"+++ /dev/null\n" +
		"@@ -1,2 +0,0 @@\n" +
		"-bye\n" +
		"-now\n"

	stats := parseDiffStats(diff)
	if stats.additions != 2 {
		t.Errorf("additions = %d, want 2 (headers must not inflate counts)", stats.additions)
	}
	if stats.deletions != 2 {
		t.Errorf("deletions = %d, want 2 (headers must not inflate counts)", stats.deletions)
	}
	want := map[string]bool{"new.go": false, "gone.go": false}
	for _, f := range stats.files {
		if _, ok := want[f]; !ok {
			t.Errorf("unexpected file %q", f)
			continue
		}
		want[f] = true
	}
	for f, seen := range want {
		if !seen {
			t.Errorf("file %q missing from stats.files", f)
		}
	}
}

// #3956 adjacency path: a "+++"-shaped line NOT immediately following a
// consumed minus-side header is content and must be counted, even when the
// hunk contains a mix of normal and header-shaped lines.
func TestParseDiffStatsNestedPatchTailContent(t *testing.T) {
	diff := "diff --git a/patch.txt b/patch.txt\n" +
		"index 111..222 100644\n" +
		"--- a/patch.txt\n" +
		"+++ b/patch.txt\n" +
		"@@ -1,2 +1,3 @@\n" +
		" outer context\n" +
		"++@@ -1,1 +1,1 @@\n" + // added inner-patch hunk header (++ prefix, not +++)
		"+++tail\n" + // added line whose content is "++tail" (three-plus shape)
		"+-meta\n" // added line whose content is "-meta"

	stats := parseDiffStats(diff)
	if stats.additions != 3 {
		t.Errorf("additions = %d, want 3 (++@@, +++tail, --meta all content)", stats.additions)
	}
	if stats.deletions != 0 {
		t.Errorf("deletions = %d, want 0", stats.deletions)
	}
}
