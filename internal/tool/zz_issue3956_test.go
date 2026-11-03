package tool

import "testing"

func TestParseDiffStatsCountsHeaderShapedContent(t *testing.T) {
	diff := "diff --git a/count.go b/count.go\n" +
		"index 111..222 100644\n" +
		"--- a/count.go\n" +
		"+++ b/count.go\n" +
		"@@ -1,3 +1,4 @@\n" +
		" context\n" +
		"+++count;\n" +
		"+plain\n" +
		"---count;\n" +
		"-----\n"

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

func TestParseDiffStatsNestedPatchTailContent(t *testing.T) {
	diff := "diff --git a/patch.txt b/patch.txt\n" +
		"index 111..222 100644\n" +
		"--- a/patch.txt\n" +
		"+++ b/patch.txt\n" +
		"@@ -1,2 +1,3 @@\n" +
		" outer context\n" +
		"++@@ -1,1 +1,1 @@\n" +
		"+++tail\n" +
		"+-meta\n"

	stats := parseDiffStats(diff)
	if stats.additions != 3 {
		t.Errorf("additions = %d, want 3 (++@@, +++tail, --meta all content)", stats.additions)
	}
	if stats.deletions != 0 {
		t.Errorf("deletions = %d, want 0", stats.deletions)
	}
}
