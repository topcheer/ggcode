package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #4657: archiveAndTombstone must not leave a half-deleted state. The rename
// IS the removal; if the tombstone write faults afterwards (ENOSPC / EACCES /
// read-only remount), the rollback restores the live file so:
//   - the caller's error means "still live", not "gone without tombstone";
//   - a retry takes the normal full-delete path instead of reporting
//     "memory not found" (Stat miss on a vanished live file);
//   - the #3880 invariant (as_of reads see the deletion gap) is never
//     silently bypassed by a missing tombstone.

// issue4657MakeHdirUnwritable makes the .history dir reject new tombstone
// writes (permission fault) while the live dir stays writable.
func issue4657MakeHdirUnwritable(t *testing.T, am *AutoMemory) {
	t.Helper()
	hdir := filepath.Join(am.dir, historyDirName)
	// Ensure hdir exists (first deletion MkdirAll) then strip write bits.
	if err := os.MkdirAll(hdir, 0o755); err != nil {
		t.Fatalf("mkdir history: %v", err)
	}
	if err := os.Chmod(hdir, 0o500); err != nil {
		t.Fatalf("chmod history read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(hdir, 0o755) })
}

func issue4657LivePath(am *AutoMemory, key string) string {
	return filepath.Join(am.dir, disambiguateKey(key, sanitizeKey(key))+".md")
}

func TestIssue4657TombstoneFaultRollsBackRename(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based fault injection does not fail for root")
	}
	am := NewAutoMemory()
	am.dir = t.TempDir()
	mustSaveMemory(t, am, "victim-key", "content that must survive a tombstone fault")

	// The live save creates no .history dir yet; create it read-only so the
	// post-rename tombstone WriteFile faults with EACCES.
	issue4657MakeHdirUnwritable(t, am)

	err := am.DeleteMemory("victim-key")
	if err == nil {
		t.Fatal("delete unexpectedly succeeded with a read-only history dir")
	}
	// Rollback must have restored the live file - NOT the half-deleted state.
	if _, statErr := os.Stat(issue4657LivePath(am, "victim-key")); statErr != nil {
		t.Fatalf("live file missing after failed delete (half-deleted state): %v", statErr)
	}
	// The memory must still be readable through the normal API.
	if got, err := am.LoadKey("victim-key"); err != nil || !strings.Contains(got, "content that must survive") {
		t.Fatalf("LoadKey after rollback = (%q, %v), want the original content", got, err)
	}
}

func TestIssue4657RetryAfterFaultCompletesFullDelete(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based fault injection does not fail for root")
	}
	am := NewAutoMemory()
	am.dir = t.TempDir()
	mustSaveMemory(t, am, "retry-key", "first")

	issue4657MakeHdirUnwritable(t, am)
	if err := am.DeleteMemory("retry-key"); err == nil {
		t.Fatal("expected fault on first delete")
	}

	// Fault clears (writable again): the retry must take the NORMAL path -
	// archive + tombstone + no "memory not found" (the pre-fix retry hit the
	// Stat miss on the vanished live file).
	if err := os.Chmod(filepath.Join(am.dir, historyDirName), 0o755); err != nil {
		t.Fatalf("clear fault: %v", err)
	}
	if err := am.DeleteMemory("retry-key"); err != nil {
		t.Fatalf("retry after fault clearance failed: %v", err)
	}
	if _, statErr := os.Stat(issue4657LivePath(am, "retry-key")); !os.IsNotExist(statErr) {
		t.Fatalf("live file still present after successful retry: %v", statErr)
	}
	if got, err := am.LoadKey("retry-key"); err != nil || got != "" {
		t.Fatalf("LoadKey after completed delete = (%q, %v), want empty", got, err)
	}
}
