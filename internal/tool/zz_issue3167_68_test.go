package tool

// Regression probes for #3167 + #3168 (diff_scan, one PR).
//
// #3167: combined-diff hunks (@@@, emitted for unmerged paths during
// cherry-pick/rebase) prefix added lines with "++". Stripping only one
// marker left the extra '+' glued to the content, so ^-anchored patterns
// (conflict markers, ^import pdb) systematically missed in the exact
// scenario where conflict-marker detection matters most.
//
// #3168: \bdd( is Python-only; a custom .dd(x) method in Go/Rust files
// fired an info-level debug-stmt false positive.

import (
	"strings"
	"testing"
)

// #3167 core: anchored patterns must hit added lines inside a combined
// hunk, with correct new-file line numbers.
func TestIssue3167_CombinedHunkAnchoredPatterns(t *testing.T) {
	diff := "diff --git a/conflict.go b/conflict.go\n" +
		"--- a/conflict.go\n" +
		"+++ b/conflict.go\n" +
		"@@@ -10,5 -8,5 +12,6 @@@\n" +
		"  base\n" +
		"++<<<<<<< HEAD\n" +
		"++=======\n" +
		"++>>>>>>> other\n"
	issues := ScanStagedDiffForIssues(diff)
	var markers int
	for _, is := range issues {
		if is.Category == "merge-conflict" {
			markers++
			if is.Line != 13 && is.Line != 14 && is.Line != 15 {
				t.Errorf("conflict marker line %d, want 13-15: %+v", is.Line, is)
			}
		}
	}
	if markers != 3 {
		t.Fatalf("combined-hunk conflict markers: got %d, want 3 (%+v)", markers, issues)
	}
}

// #3167: ^import pdb (anchored debugger pattern) must fire in a combined
// hunk.
func TestIssue3167_CombinedHunkDebuggerAnchor(t *testing.T) {
	diff := "--- a/x.py\n+++ b/x.py\n@@@ -1 +1,2 @@@\n++import pdb; pdb.set_trace()\n"
	issues := ScanStagedDiffForIssues(diff)
	found := false
	for _, is := range issues {
		if is.Category == "debugger" {
			found = true
		}
	}
	if !found {
		t.Fatalf("anchored pdb pattern missed in combined hunk: %+v", issues)
	}
}

// #3167 regression: plain hunks still single-strip and count correctly.
func TestIssue3167_PlainHunkUnchanged(t *testing.T) {
	diff := "--- a/x.go\n+++ b/x.go\n@@ -3,2 +3,2 @@\n ctx\n+fmt.Println(\"debug\")\n"
	issues := ScanStagedDiffForIssues(diff)
	found := false
	for _, is := range issues {
		if is.Category == "debug-stmt" && is.Line == 4 {
			found = true
		}
	}
	if !found {
		t.Fatalf("plain-hunk debug-stmt regression: %+v", issues)
	}
}

// #3168: .dd( method call in a Go file must NOT fire debug-stmt; in a
// Python file it still does.
func TestIssue3168_DDGatedToPython(t *testing.T) {
	goDiff := "--- a/svc.go\n+++ b/svc.go\n@@ -1 +1 @@\n+result := registry.dd(x)\n"
	for _, is := range ScanStagedDiffForIssues(goDiff) {
		if is.Category == "debug-stmt" && strings.Contains(is.Message, "dd") {
			t.Fatalf(".dd( fired on a Go file (#3168): %+v", is)
		}
	}
	pyDiff := "--- a/t.py\n+++ b/t.py\n@@ -1 +1 @@\n+dd(payload)\n"
	found := false
	for _, is := range ScanStagedDiffForIssues(pyDiff) {
		if is.Category == "debug-stmt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("dd( must still fire on Python files: %+v", ScanStagedDiffForIssues(pyDiff))
	}
}
