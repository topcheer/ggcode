package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func issue4657MakeHdirUnwritable(t *testing.T, am *AutoMemory) {
	t.Helper()
	hdir := filepath.Join(am.dir, historyDirName)

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

	issue4657MakeHdirUnwritable(t, am)

	err := am.DeleteMemory("victim-key")
	if err == nil {
		t.Fatal("delete unexpectedly succeeded with a read-only history dir")
	}

	if _, statErr := os.Stat(issue4657LivePath(am, "victim-key")); statErr != nil {
		t.Fatalf("live file missing after failed delete (half-deleted state): %v", statErr)
	}

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
