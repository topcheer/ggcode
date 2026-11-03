package memory

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func issue4658Setup(t *testing.T) *AutoMemory {
	t.Helper()
	am := NewAutoMemory()
	am.dir = t.TempDir()
	return am
}

func TestIssue4658PruneKeepsMarkForRecreatedFile(t *testing.T) {
	am := issue4658Setup(t)
	mustSaveMemory(t, am, "revive-key", "re-created after digest")

	if err := am.ForgetKey("revive-key"); err != nil {
		t.Fatalf("forget: %v", err)
	}
	am.pruneDigestedForgotten([]string{"revive-key"})

	if _, ok := am.loadForgotten().Entries["revive-key"]; !ok {
		t.Fatal("mark pruned for a live re-created file - entry would revive into prompt injection")
	}

	path := filepath.Join(am.dir, "revive-key.md")
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove live file: %v", err)
	}
	am.pruneDigestedForgotten([]string{"revive-key"})
	if _, ok := am.loadForgotten().Entries["revive-key"]; ok {
		t.Fatal("mark kept for a digested (absent) file - ghost-restore window")
	}
}

func TestIssue4658ForgetKeyNoGhostWhenFileDiesDuringLockWait(t *testing.T) {
	am := issue4658Setup(t)
	mustSaveMemory(t, am, "ghost-key", "doomed")

	unlock := am.lockForgottenWrite()

	path := filepath.Join(am.dir, "ghost-key.md")
	if err := os.Rename(path, filepath.Join(am.dir, "ghost-key.digest.md")); err != nil {
		t.Fatalf("simulate digest rename: %v", err)
	}

	var (
		wg   sync.WaitGroup
		errR error
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		errR = am.ForgetKey("ghost-key")
	}()

	time.Sleep(150 * time.Millisecond)
	unlock()
	wg.Wait()

	if errR == nil || !strings.Contains(errR.Error(), "not found") {
		t.Fatalf("ForgetKey err = %v, want \"not found\" (file gone under the lock)", errR)
	}
	if _, ok := am.loadForgotten().Entries["ghost-key"]; ok {
		t.Fatal("ghost mark written for a digested file - permanent ListForgotten/RestoreKey ghost")
	}
}
