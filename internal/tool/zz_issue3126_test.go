package tool

import (
	"context"
	"strings"
	"testing"
	"time"
)

// #3126 probes, three parts.

// --- Main: parseFileChanges must register rename-only / binary / pure
// mode-change files (a partition plan that omits them silently skips
// staging them).

func TestIssue3126_ParseRenameOnlyDiff(t *testing.T) {
	diff := "diff --git a/old.go b/new.go\n" +
		"similarity index 100%\n" +
		"rename from old.go\n" +
		"rename to new.go\n"
	files := parseFileChanges(diff)
	if len(files) != 1 || files[0].Path != "new.go" {
		t.Fatalf("rename-only file lost from partition plan: %+v", files)
	}
}

func TestIssue3126_ParseBinaryDiff(t *testing.T) {
	diff := "diff --git a/logo.png b/logo.png\n" +
		"index 1234567..89abcde 100644\n" +
		"Binary files a/logo.png and b/logo.png differ\n"
	files := parseFileChanges(diff)
	if len(files) != 1 || files[0].Path != "logo.png" {
		t.Fatalf("binary file lost from partition plan: %+v", files)
	}
}

func TestIssue3126_ParseModeChangeDiff(t *testing.T) {
	diff := "diff --git a/script.sh b/script.sh\n" +
		"old mode 100644\n" +
		"new mode 100755\n"
	files := parseFileChanges(diff)
	if len(files) != 1 || files[0].Path != "script.sh" {
		t.Fatalf("mode-only file lost from partition plan: %+v", files)
	}
}

// Mixed: renames must coexist with normal text hunks in one diff.
func TestIssue3126_MixedRenameAndTextDiff(t *testing.T) {
	diff := "diff --git a/renamed.go b/renamed.go\n" +
		"similarity index 90%\n" +
		"rename from orig.go\n" +
		"rename to renamed.go\n" +
		"index 111..222 100644\n" +
		"--- a/orig.go\n" +
		"+++ b/renamed.go\n" +
		"@@ -1 +1 @@\n" +
		"-old\n" +
		"+new\n" +
		"diff --git a/other.go b/other.go\n" +
		"--- a/other.go\n" +
		"+++ b/other.go\n" +
		"@@ -1 +1 @@\n" +
		"-x\n" +
		"+y\n"
	files := parseFileChanges(diff)
	var renamed, other bool
	for _, f := range files {
		if f.Path == "renamed.go" {
			renamed = true
			if f.Additions != 1 || f.Deletions != 1 {
				t.Fatalf("renamed.go counts wrong: +%d/-%d", f.Additions, f.Deletions)
			}
		}
		if f.Path == "other.go" {
			other = true
		}
	}
	if !renamed || !other {
		t.Fatalf("mixed diff parse lost files: %+v", files)
	}
}

// --- A: failed jobs must enter the eviction ledger, not squat m.jobs.

func TestIssue3126_FailedJobsAreEvicted(t *testing.T) {
	m := NewCommandJobManager("")
	// A shell command ggcode cannot resolve fails at NewShellCommandContext
	// time - the Start failure path. Fire more than maxFinishedJobs of them.
	dud := "\x00invalid-shell-syntax\x00"
	for i := 0; i < maxFinishedJobs+5; i++ {
		snap, err := m.Start(context.Background(), dud, false, 0)
		if err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
		if snap.Status != CommandJobFailed {
			t.Fatalf("expected immediate failure, got %v", snap.Status)
		}
	}
	m.mu.Lock()
	live := len(m.jobs)
	fin := len(m.finishedOrder)
	m.mu.Unlock()
	if live > maxFinishedJobs {
		t.Fatalf("#3126 A: failed jobs leak in m.jobs: %d live (cap %d), finishedOrder=%d", live, maxFinishedJobs, fin)
	}
}

// --- B: start marker exactness.

func TestIssue3126_ConflictStartMarkerExactness(t *testing.T) {
	// A documentation/fixture example with a longer marker run must NOT
	// open a conflict region.
	doc := "some doc line\n" +
		"<<<<<<<< eight markers are not a conflict marker\n" +
		"trailing\n"
	if regions := DetectMergeConflicts(doc); len(regions) != 0 {
		t.Fatalf("8-char marker opened a phantom region: %+v", regions)
	}
	// The real form - bare marker or marker + space + label - must work.
	for _, marker := range []string{"<<<<<<<\n", "<<<<<<< HEAD\n"} {
		doc := "a\n" + marker + "======\n" + ">>>>>>> other\n"
		if regions := DetectMergeConflicts(doc); len(regions) != 1 {
			t.Fatalf("real marker %q not detected: %+v", strings.TrimSpace(marker), regions)
		}
	}
}

// Guard against flake: eviction keeps the most recent finished jobs.
func TestIssue3126_EvictionKeepsRecent(t *testing.T) {
	m := NewCommandJobManager("")
	dud := "\x00invalid-shell-syntax\x00"
	for i := 0; i < 3; i++ {
		if _, err := m.Start(context.Background(), dud, false, 0); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	m.mu.Lock()
	live := len(m.jobs)
	m.mu.Unlock()
	if live != 3 {
		t.Fatalf("recent failed jobs should be retained (got %d, want 3)", live)
	}
}
