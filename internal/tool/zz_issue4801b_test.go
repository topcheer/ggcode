package tool

import (
	"strings"
	"testing"
)

func TestIssue4801B_CombinedSinglePlusConflictMarkerDetected(t *testing.T) {
	diff := "diff --cc file.txt\nindex 111..222 100644\n--- a/file.txt\n+++ b/file.txt\n" +
		"@@@ -1,3 -1,3 +1,3 @@@\n" +
		"  base line\n" +
		"+ <<<<<<< HEAD\n" +
		"+ >>>>>>> branch\n"
	issues := ScanStagedDiffForIssues(diff)
	found := false
	for _, iss := range issues {
		if strings.Contains(iss.Message, "conflict") || strings.Contains(iss.Message, "<<") {
			found = true
		}
	}
	if !found {
		t.Fatalf("single-plus combined conflict markers must be flagged; issues=%+v", issues)
	}
}

func TestIssue4801B_PlainHunkConflictMarkerStillDetected(t *testing.T) {
	diff := "diff --git a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n" +
		"@@ -1,2 +1,2 @@\n" +
		" ctx\n" +
		"+<<<<<<< HEAD\n"
	issues := ScanStagedDiffForIssues(diff)
	found := false
	for _, iss := range issues {
		if strings.Contains(iss.Message, "conflict") || strings.Contains(iss.Message, "<<") {
			found = true
		}
	}
	if !found {
		t.Fatalf("plain-hunk conflict marker must still be flagged; issues=%+v", issues)
	}
}

func TestIssue4801B_CombinedDoublePlusContentScanned(t *testing.T) {
	diff := "diff --cc file.txt\n--- a/file.txt\n+++ b/file.txt\n" +
		"@@@ -1,2 -1,2 +1,2 @@@\n" +
		"++<<<<<<< HEAD\n"
	issues := ScanStagedDiffForIssues(diff)
	found := false
	for _, iss := range issues {
		if strings.Contains(iss.Message, "conflict") || strings.Contains(iss.Message, "<<") {
			found = true
		}
	}
	if !found {
		t.Fatalf("double-plus combined conflict marker must be flagged; issues=%+v", issues)
	}
}
