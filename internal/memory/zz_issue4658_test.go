package memory

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// #4658 companion tests: forget-mark lifecycle double gap.
//   A. GC prune must not delete a mark whose live file was re-created after
//      the digest snapshot (revival race, ms..s window between unlockGC and
//      the sidecar prune).
//   B. ForgetKey's existence check must run under the sidecar write lock; a
//      file digested during the (up-to-10s) lock wait must yield "not
//      found", never a permanent ghost mark.

func issue4658Setup(t *testing.T) *AutoMemory {
	t.Helper()
	am := NewAutoMemory()
	am.dir = t.TempDir()
	return am
}

func TestIssue4658PruneKeepsMarkForRecreatedFile(t *testing.T) {
	am := issue4658Setup(t)
	mustSaveMemory(t, am, "revive-key", "re-created after digest")

	// Simulate the race's end state: the digest snapshot listed the key, but
	// by prune time the user has re-saved the file AND re-marked it
	// forgotten. The mark is NEW - pruning it would revive the entry into
	// prompt injection.
	if err := am.ForgetKey("revive-key"); err != nil {
		t.Fatalf("forget: %v", err)
	}
	am.pruneDigestedForgotten([]string{"revive-key"})

	if _, ok := am.loadForgotten().Entries["revive-key"]; !ok {
		t.Fatal("mark pruned for a live re-created file - entry would revive into prompt injection")
	}
	// Sanity: with the file actually gone (the normal digest end state) the
	// mark IS pruned.
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

	// Hold the sidecar write lock, simulating a concurrent holder. The
	// pre-fix ForgetKey stat ran BEFORE this lock - a file digested while
	// ForgetKey busy-waited (window up to 10s per #4426) still got marked.
	unlock := am.lockForgottenWrite()

	// The GC digests the file while ForgetKey waits for the lock.
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
	// Let the goroutine reach (and block on) the lock.
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
