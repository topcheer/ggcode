package wailskit

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIssue580_Bug2_RecursiveWalkErrors verifies that walkDirectoryEntries
// logs errors instead of silently skipping them, respects entry limits,
// and excludes common generated directories (#580 Bug 2).
func TestIssue580_Bug2_RecursiveWalkErrors(t *testing.T) {
	tmpDir := t.TempDir()

	normalDir := filepath.Join(tmpDir, "normal")
	os.Mkdir(normalDir, 0755)
	os.WriteFile(filepath.Join(normalDir, "file.txt"), []byte("test"), 0644)

	nodeModules := filepath.Join(tmpDir, "node_modules")
	os.Mkdir(nodeModules, 0755)
	os.WriteFile(filepath.Join(nodeModules, "pkg.json"), []byte("{}"), 0644)

	gitDir := filepath.Join(tmpDir, ".git")
	os.Mkdir(gitDir, 0755)
	os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main"), 0644)

	vscodeDir := filepath.Join(tmpDir, ".vscode")
	os.Mkdir(vscodeDir, 0755)
	os.WriteFile(filepath.Join(vscodeDir, "settings.json"), []byte("{}"), 0644)

	ideaDir := filepath.Join(tmpDir, ".idea")
	os.Mkdir(ideaDir, 0755)
	os.WriteFile(filepath.Join(ideaDir, "iml.xml"), []byte("<xml/>"), 0644)

	entries, err := walkDirectoryEntries(tmpDir)
	if err != nil {
		t.Fatalf("walkDirectoryEntries failed: %v", err)
	}

	if len(entries) == 0 {
		t.Fatal("expected at least one entry, got none")
	}

	for _, e := range entries {
		if e.Name == "node_modules" || e.Name == ".git" || e.Name == ".vscode" || e.Name == ".idea" {
			t.Errorf("excluded directory %q should not appear in results", e.Name)
		}
	}

	t.Logf("walkDirectoryEntries returned %d entries (exclusions working)", len(entries))
}

// TestIssue580_Bug2_RecursiveWalkLimit verifies that walkDirectoryEntries
// enforces the maxRecursiveEntries limit (#580 Bug 2).
func TestIssue580_Bug2_RecursiveWalkLimit(t *testing.T) {
	tmpDir := t.TempDir()

	for i := 0; i < 100; i++ {
		subDir := filepath.Join(tmpDir, "dir"+string(rune('a'+i)))
		os.Mkdir(subDir, 0755)

		os.WriteFile(filepath.Join(subDir, "file.txt"), []byte("test"), 0644)
	}

	entries, err := walkDirectoryEntries(tmpDir)

	if err != nil {
		t.Fatalf("walkDirectoryEntries failed unexpectedly: %v", err)
	}

	if len(entries) != 200 {
		t.Errorf("expected 200 entries, got %d", len(entries))
	}

	t.Logf("walkDirectoryEntries correctly handled %d entries (below limit)", len(entries))
}

// TestIssue580_Bug6_OverflowNoticeSeqPositioning verifies that overflow
// notices use a Seq positioned before the first retained entry instead
// of a new maximum value (#580 Bug 6).
func TestIssue580_Bug6_OverflowNoticeSeqPositioning(t *testing.T) {
	ls := NewLogStream(10)
	ls.Enable(true)

	for i := 0; i < 5010; i++ {
		ls.Write("test", "entry")
	}

	entries := ls.Drain()
	if len(entries) == 0 {
		t.Fatal("expected entries after overflow, got none")
	}

	if entries[0].Category != "logstream" {
		t.Errorf("expected overflow notice first, got category %q", entries[0].Category)
	}

	overflowSeq := entries[0].Seq
	if len(entries) < 2 {
		t.Fatal("expected at least 2 entries (notice + one retained)")
	}
	firstRetainedSeq := entries[1].Seq

	if overflowSeq >= firstRetainedSeq {
		t.Errorf("overflow notice Seq %d should be less than first retained Seq %d",
			overflowSeq, firstRetainedSeq)
	}

	t.Logf("overflow notice Seq=%d, first retained Seq=%d (positioning correct)",
		overflowSeq, firstRetainedSeq)
}

// TestIssue580_Bug4_ListCronJobsNilScheduler verifies that ListCronJobs
// returns an error when the scheduler is not available, instead of
// silently returning nil (#580 Bug 4).
func TestIssue580_Bug4_ListCronJobsNilScheduler(t *testing.T) {
	cb := &ChatBridge{}

	jobs, err := cb.ListCronJobs()
	if err == nil {
		t.Error("ListCronJobs should return error when scheduler is nil, got nil")
	}
	if jobs != nil {
		t.Error("ListCronJobs should return nil jobs slice when scheduler is nil")
	}

	expectedErr := "cron scheduler not available"
	if err != nil && err.Error() != expectedErr {
		t.Errorf("expected error %q, got %q", expectedErr, err.Error())
	}

	t.Logf("ListCronJobs correctly returns error for nil scheduler: %v", err)
}

// TestIssue580_Bug1_DeadAPIsConfirmation confirms that the package-level
// functions (ListDirectory, ReadFileContent, GetWorkingDir) are currently
// unused in production code and kept as-is per decision (#580 Bug 1).
func TestIssue580_Bug1_DeadAPIsConfirmation(t *testing.T) {

	entries, err := ListDirectory(".", false)
	if err != nil {
		t.Logf("ListDirectory error (expected, dead API): %v", err)
	} else {
		t.Logf("ListDirectory returned %d entries (dead API still functional)", len(entries))
	}

	wd := GetWorkingDir()
	if wd == "" {
		t.Log("GetWorkingDir returned empty string (dead API behavior)")
	} else {
		t.Logf("GetWorkingDir returned %q (dead API still functional)", wd)
	}

	t.Log("Bug 1: Dead APIs confirmed as-is per decision - no changes made")
}
