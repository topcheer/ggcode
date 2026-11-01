package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #4791 (S-21): a command session whose workingDir was inside a REMOVED
// worktree failed every subsequent spawn with ENOENT-class errors and a
// cd prefix could not save it. NearestLiveDir + the Start guard respawn at
// the nearest live ancestor instead.
func TestIssue4791_NearestLiveDir(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "wt", "inner")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := NearestLiveDir(deep); got != deep {
		t.Fatalf("live dir must pass through, got %q", got)
	}
	if err := os.RemoveAll(deep); err != nil {
		t.Fatal(err)
	}
	if got := NearestLiveDir(deep); got != filepath.Join(root, "wt") {
		t.Fatalf("dead dir must resolve to nearest live ancestor %q, got %q", filepath.Join(root, "wt"), got)
	}
	if got := NearestLiveDir(""); got != "" {
		t.Fatalf("empty must stay empty, got %q", got)
	}
}

func TestIssue4791_StartRecoversAfterWorkdirRemoved(t *testing.T) {
	root := t.TempDir()
	dead := filepath.Join(root, "worktree-xyz")
	if err := os.MkdirAll(dead, 0o755); err != nil {
		t.Fatal(err)
	}
	m := NewCommandJobManager(dead)
	defer m.ShutdownAll(2 * time.Second)

	// Healthy spawn inside the (still alive) working dir.
	snap, err := m.Start(context.Background(), "pwd", false, 10*time.Second)
	if err != nil {
		t.Fatalf("healthy start: %v", err)
	}
	final, err := m.Wait(context.Background(), snap.ID, 10*time.Second, 5, 0)
	if err != nil {
		t.Fatalf("healthy wait: %v", err)
	}
	if final.Status != CommandJobCompleted {
		t.Fatalf("healthy job must succeed, got %s (%s)", final.Status, final.ErrText)
	}

	// The worktree is removed mid-session (S-21 trigger).
	if err := os.RemoveAll(dead); err != nil {
		t.Fatal(err)
	}

	// The next spawn must RECOVER: run at the nearest live ancestor
	// instead of failing every command forever.
	snap2, err := m.Start(context.Background(), "pwd", false, 10*time.Second)
	if err != nil {
		t.Fatalf("recovery start: %v", err)
	}
	final2, err := m.Wait(context.Background(), snap2.ID, 10*time.Second, 5, 0)
	if err != nil {
		t.Fatalf("recovery wait: %v", err)
	}
	if final2.Status != CommandJobCompleted {
		t.Fatalf("post-removal spawn must recover, got %s (%s)", final2.Status, final2.ErrText)
	}
	out := strings.Join(final2.Lines, "\n")
	if !strings.Contains(out, filepath.Base(root)) {
		t.Fatalf("recovered spawn should run at the live ancestor %q, output: %q", root, out)
	}
}
