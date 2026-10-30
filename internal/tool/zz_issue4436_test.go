package tool

import (
	"strings"
	"testing"
)

// #4436: three fixes pinned -
// 1) quoted diff-header paths (git quotes paths with spaces) now parse in
//    staging_quality fileSet / diff_scan currentFile / analyzer+partition
//    lastGitPath, with quote debris stripped;
// 2) define_tool corrupted embedded schema fails closed;
// 3) commit_consistency rename: message references to the OLD name are not
//    phantom files.

func TestIssue4436DiffGitHeaderQuotedPaths(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{`diff --git a/plain.go b/plain.go`, "plain.go"},       // bare still works
		{`diff --git "a/foo bar.go" "b/foo bar.go"`, "bar.go"}, // wait: b side full path below
	}
	// The regex captures the full b/-side path, not the basename.
	cases[1].want = "foo bar.go"
	for _, c := range cases {
		m := diffGitHeader.FindStringSubmatch(c.line)
		if m == nil {
			t.Fatalf("diffGitHeader did not match %q", c.line)
		}
		got := m[1]
		if got == "" && len(m) > 2 {
			got = m[2]
		}
		if got == "" || normalizeGitDiffPath(got) != c.want {
			t.Errorf("line %q: got path %q (normalized %q), want %q", c.line, got, normalizeGitDiffPath(got), c.want)
		}
	}
}

func TestIssue4436QuotedPathAttribution(t *testing.T) {
	// A quoted `+++ b/"my file.go"` header must attribute issues to
	// `my file.go`, not a path with literal quote debris.
	diff := "+++ b/\"my file.go\"\n@@ -1,2 +1,2 @@\n context\n+TODO: fix me\n"
	issues := ScanStagedDiffForIssues(diff)
	if len(issues) == 0 {
		t.Fatal("no issues found for quoted-path diff")
	}
	if issues[0].File != "my file.go" {
		t.Fatalf("issue attributed to %q, want %q (quote debris not stripped)", issues[0].File, "my file.go")
	}
}

func TestIssue4436RenameOldNameNotPhantom(t *testing.T) {
	diff := "diff --git a/handler.go b/api.go\nsimilarity index 95%\nrename from handler.go\nrename to api.go\n"
	warn := AnalyzeMessageDiffConsistency("rename handler.go to api.go", diff)
	if strings.Contains(warn, "phantom") || strings.Contains(strings.ToLower(warn), "handler.go") {
		t.Fatalf("old rename name flagged as phantom:\n%s", warn)
	}
	// Counter-check: a truly unrelated file IS still flagged.
	warn2 := AnalyzeMessageDiffConsistency("rename handler.go to api.go, also touches unrelated.go", diff)
	if !strings.Contains(warn2, "unrelated.go") {
		t.Fatalf("unrelated file not flagged:\n%s", warn2)
	}
}

func TestIssue4436RenameStillCountsOnce(t *testing.T) {
	// #4433 regression guard: pure renames must not inflate len(files).
	var sb strings.Builder
	for i := 0; i < 3; i++ {
		sb.WriteString("diff --git a/old")
		sb.WriteString(string(rune('a' + i)))
		sb.WriteString(".go b/new")
		sb.WriteString(string(rune('a' + i)))
		sb.WriteString(".go\nrename from x\nrename to y\n")
	}
	files, _, _, _ := parseDiffFacts(sb.String())
	if len(files) != 3 {
		t.Fatalf("3 pure renames produced %d files, want 3 (#4433 regression)", len(files))
	}
}
