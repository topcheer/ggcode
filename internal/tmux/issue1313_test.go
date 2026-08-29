package tmux

// Regression test for GitHub issue #1313: lockTmuxStore claimed to be a
// "cross-process lock" but was a package-level sync.Mutex - two ggcode
// terminals saving the shared ~/.ggcode/tmux-panes.json raced
// read-modify-write and the last writer silently dropped the other's
// workspace panes. The lock is now a real flock/LockFileEx on a sidecar,
// contended even between two fds of the SAME process (flock is per
// open-file-description), which this test exploits.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIssue1313_LockBlocksSecondAcquirer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tmux-panes.json")

	unlock1, err := lockTmuxStore(path)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		unlock2, err := lockTmuxStore(path)
		if err != nil {
			t.Errorf("second acquire: %v", err)
			return
		}
		close(acquired)
		unlock2()
	}()

	// While held, a second acquirer must NOT get the lock.
	select {
	case <-acquired:
		t.Fatal("#1313: second lock acquired while first still held - mutual exclusion broken")
	case <-time.After(150 * time.Millisecond):
	}

	unlock1()

	// After release the second acquirer proceeds.
	select {
	case <-acquired:
	case <-time.After(3 * time.Second):
		t.Fatal("second lock never acquired after release (deadlock)")
	}
}

// Unique tmp names: concurrent saves must not rename away each other's
// temp files (#1313 second half - fixed path+".tmp" was shared).
func TestIssue1313_SaveKeepsWorkspaceEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tmux-panes.json")

	panesA := []Pane{{ID: "%1", Workspace: "wsA", Command: "a"}}
	panesB := []Pane{{ID: "%2", Workspace: "wsB", Command: "b"}}

	// Sequential saves to the same store keep both workspaces (the
	// read-modify-write path the lock now guards).
	if err := saveWorkspaceState(path, "wsA", panesA, nil); err != nil {
		t.Fatalf("save wsA: %v", err)
	}
	if err := saveWorkspaceState(path, "wsB", panesB, nil); err != nil {
		t.Fatalf("save wsB: %v", err)
	}

	gotA, _, err := loadWorkspaceState(path, "wsA")
	if err != nil || len(gotA) != 1 {
		t.Fatalf("wsA lost after wsB save: panes=%v err=%v", gotA, err)
	}
	gotB, _, err := loadWorkspaceState(path, "wsB")
	if err != nil || len(gotB) != 1 {
		t.Fatalf("wsB missing: panes=%v err=%v", gotB, err)
	}

	// No leftover temp files with the old fixed name.
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Fatal("fixed-name .tmp left behind")
	}
}
