package tool

import (
	"strings"
	"testing"
)

func TestIssue3133_CombinedHunkHeaderLineNumbers(t *testing.T) {
	diff := "diff --git a/conflict.go b/conflict.go\n" +
		"--- a/conflict.go\n" +
		"+++ b/conflict.go\n" +
		"@@@ -10,5 -8,5 +12,6 @@@\n" +
		" base\n" +
		"+added line with TODO: fix me\n" +
		" ctx\n"
	issues := ScanStagedDiffForIssues(diff)
	var found bool
	for _, is := range issues {
		if strings.Contains(is.Message, "TODO") && is.Line == 13 {
			found = true
		}
	}
	if !found {
		t.Fatalf("combined-hunk issue not reported at new-file line 13: %+v", issues)
	}
}

func TestIssue3133_PlainHunkStillWorks(t *testing.T) {
	diff := "--- a/x.go\n+++ b/x.go\n@@ -3,2 +3,2 @@\n ctx\n+TODO: plain\n"
	issues := ScanStagedDiffForIssues(diff)
	var found bool
	for _, is := range issues {
		if strings.Contains(is.Message, "TODO") && is.Line == 4 {
			found = true
		}
	}
	if !found {
		t.Fatalf("plain-hunk regression: %+v", issues)
	}
}

func TestIssue3133_CombinedHeaderResetsCounter(t *testing.T) {
	diff := "--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n+TODO: first\n" +
		"--- a/b.go\n+++ b/b.go\n@@@ -1 +1,2 @@@\n ctx\n+TODO: second\n"
	issues := ScanStagedDiffForIssues(diff)
	var firstOnA, secondOnB bool
	for _, is := range issues {
		if strings.Contains(is.File, "a.go") && is.Line == 1 {
			firstOnA = true
		}
		if strings.Contains(is.File, "b.go") && is.Line == 2 {
			secondOnB = true
		}
	}
	if !firstOnA || !secondOnB {
		t.Fatalf("line misplacement: a.go@1=%v b.go@2=%v issues=%+v", firstOnA, secondOnB, issues)
	}
}
