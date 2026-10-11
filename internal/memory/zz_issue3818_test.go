package memory

// #3818 companion: archiveVersion now reports the archive destination so
// the save path can roll the live file back when the swap-in rename fails.
// These tests pin the return contract and the rollback primitive; the
// end-to-end failure injection (cross-device/full-disk) is not portable.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newAutoMemory3818(t *testing.T) *AutoMemory {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return &AutoMemory{dir: dir}
}

func TestIssue3818_ArchiveVersionReturnsDestination(t *testing.T) {
	am := newAutoMemory3818(t)
	if err := am.SaveMemory("build-process", "v1"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(am.dir, "build-process.md")
	dst, err := am.archiveVersion("build-process", path)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if dst == "" || !strings.Contains(filepath.ToSlash(dst), historyDirName+"/") {
		t.Fatalf("archive must return the .history destination, got %q", dst)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("live file must be moved away, stat err=%v", err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("archived copy missing: %v", err)
	}
}

func TestIssue3818_RollbackPrimitiveRestoresLive(t *testing.T) {
	am := newAutoMemory3818(t)
	if err := am.SaveMemory("api-gotcha", "old"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(am.dir, "api-gotcha.md")
	dst, err := am.archiveVersion("api-gotcha", path)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the failed swap-in + rollback the save path now performs.
	if err := os.Rename(dst, path); err != nil {
		t.Fatalf("rollback rename: %v", err)
	}
	cur, err := am.LoadKey("api-gotcha")
	if err != nil || cur != "old" {
		t.Fatalf("rollback must restore live content, got %q err=%v", cur, err)
	}
}

func TestIssue3818_SaveStillArchives(t *testing.T) {
	am := newAutoMemory3818(t)
	if err := am.SaveMemory("build-process", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := am.SaveMemory("build-process", "v2"); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(am.dir, historyDirName, "build-process.*.md"))
	if len(matches) < 1 {
		t.Fatalf("superseding save must archive the outgoing version, got %v", matches)
	}
	cur, err := am.LoadKey("build-process")
	if err != nil || cur != "v2" {
		t.Fatalf("live version must be v2 after save, got %q err=%v", cur, err)
	}
}
