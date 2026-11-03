package tmux

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

	select {
	case <-acquired:
		t.Fatal("#1313: second lock acquired while first still held - mutual exclusion broken")
	case <-time.After(150 * time.Millisecond):
	}

	unlock1()

	select {
	case <-acquired:
	case <-time.After(3 * time.Second):
		t.Fatal("second lock never acquired after release (deadlock)")
	}
}

func TestIssue1313_SaveKeepsWorkspaceEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tmux-panes.json")

	panesA := []Pane{{ID: "%1", Workspace: "wsA", Command: "a"}}
	panesB := []Pane{{ID: "%2", Workspace: "wsB", Command: "b"}}

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

	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Fatal("fixed-name .tmp left behind")
	}
}
