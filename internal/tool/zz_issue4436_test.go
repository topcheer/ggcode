package tool

// zz_issue4436_test.go - companion tests for #4436 (regex/contract subset).
// Bug 1: quoted-path regex gaps in staging_quality + diff_scan.
// Bug 5: rename headers must register both old and new basenames so Rule 1
// does not flag "rename handler.go to api.go" as a phantom file mention.
// Bug 2 (define_tool fail-closed) and Bug 3/4 (delegate panic backstop /
// cwd race) are concurrency/store-path changes validated by build + the
// existing suite; no deterministic unit seam without heavy harnesses.

import (
	"strings"
	"testing"
)

func TestIssue4436StagingQuotedDiffGitHeader(t *testing.T) {
	// Binary/pure-rename style header with quoted paths (spaces): the old
	// bare \S+ regex missed the whole line, silently skipping the artifact
	// check for such files.
	m := diffGitHeader.FindStringSubmatch(`diff --git "a/foo bar.go" "b/foo bar.go"`)
	if m == nil {
		t.Fatal("quoted diff --git header must match")
	}
	got := m[1]
	if got == "" {
		got = m[2]
	}
	if got != "foo bar.go" {
		t.Fatalf("quoted b/ capture = %q, want foo bar.go", got)
	}
	// Bare form still works and lands in capture 2.
	m = diffGitHeader.FindStringSubmatch("diff --git a/plain.go b/plain.go")
	if m == nil || m[1] != "" || m[2] != "plain.go" {
		t.Fatalf("bare form broken: %#v", m)
	}
}

func TestIssue4436StagingQuotedPathFlagged(t *testing.T) {
	diff := "diff --git \"a/deep dir/.DS_Store\" \"b/deep dir/.DS_Store\"\nnew file mode 100644\nindex 0000000..1111111\nBIN\n"
	out := AnalyzeStagingQuality(diff)
	if !strings.Contains(out, ".DS_Store") {
		t.Fatalf("quoted-path .DS_Store artifact must be flagged, got: %q", out)
	}
}

func TestIssue4436DiffScanQuotedFileHeaderUnquoted(t *testing.T) {
	// +++ b/"my file.go" used to carry the literal quotes into DiffIssue.File.
	diff := "--- a/x\n+++ b/\"my file.go\"\n@@ -1,2 +1,3 @@\n package x\n+\tfmt.Println(\"dbg\")\n"
	issues := ScanStagedDiffForIssues(diff)
	found := false
	for _, is := range issues {
		if strings.Contains(is.File, "\"") {
			t.Fatalf("issue file still carries quotes: %+v", is)
		}
		if is.File == "my file.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("debug stmt in quoted-path file must be attributed to \"my file.go\", issues: %+v", issues)
	}
}

func TestIssue4436RenameRegistersBothBasenames(t *testing.T) {
	// "rename handler.go to api.go" - the old name is a real subject of the
	// change; registering only the new side made Rule 1 flag it as phantom.
	// The old side routes to renameOlds (Rule 1 membership) while files
	// keeps its #4433 new-side-only gate semantics.
	diff := "diff --git a/handler.go b/api.go\nsimilarity index 100%\nrename from handler.go\nrename to api.go\n"
	files, _, renameOlds, _ := parseDiffFacts(diff)
	if _, ok := files["api.go"]; !ok {
		t.Fatalf("new basename missing from files: %v", files)
	}
	if _, ok := renameOlds["handler.go"]; !ok {
		t.Fatalf("old basename missing from renameOlds (phantom regression): %v", renameOlds)
	}
	if _, ok := files["handler.go"]; ok {
		t.Fatalf("old basename must not inflate files (#4433 gate): %v", files)
	}
	// End to end: message naming the old side is NOT a phantom.
	msg := "rename handler.go to api.go"
	if out := AnalyzeMessageDiffConsistency(msg, diff); strings.Contains(out, "handler.go") {
		t.Fatalf("old-side mention must not be flagged phantom: %q", out)
	}
	// Same-basename rename still counts once (#4433 gate intact).
	same := "diff --git a/old/util.go b/new/util.go\nsimilarity index 100%\nrename from old/util.go\nrename to new/util.go\n"
	files2, _, renameOlds2, _ := parseDiffFacts(same)
	if len(files2) != 1 || len(renameOlds2) != 0 {
		t.Fatalf("same-basename rename must count once with no extra olds, got files=%v olds=%v", files2, renameOlds2)
	}
}
