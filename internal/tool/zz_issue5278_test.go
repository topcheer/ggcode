package tool

// Issue #5278 pin tests: waitForJob used to write job.safetyHint AFTER
// finish() closed done (unlocked), so a Wait()-then-snapshot() caller raced
// the write and could observe an empty #4995 SafetyHint. The fix computes the
// hint and publishes it under job.mu BEFORE finish(). These tests pin both
// halves: the ordering (test A) and the lock discipline (test B, run -race).

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func gitCmd5278(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// Test A (#5278 ordering): a successful detach-prone `git worktree add
// <path> <commit-ish>` (two positionals, per #4995: explicit --detach is
// deliberate and intentionally NOT flagged) must carry a NON-EMPTY
// SafetyHint in the snapshot returned by Wait itself. Pre-fix this read
// raced the post-done unlocked write and came back empty.
func TestIssue5278_WaitPathSeesWorktreeSafetyHint(t *testing.T) {
	repo := t.TempDir()
	gitCmd5278(t, repo, "init", "-q")
	gitCmd5278(t, repo, "config", "user.email", "t@example.com")
	gitCmd5278(t, repo, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	gitCmd5278(t, repo, "add", "-A")
	gitCmd5278(t, repo, "commit", "-q", "-m", "init")

	wt := filepath.Join(repo, "wt-detached")
	mgr := NewCommandJobManager(repo)
	started, err := mgr.Start(context.Background(),
		fmt.Sprintf("git worktree add %s HEAD", wt), false, 30*time.Second)
	if err != nil {
		t.Fatalf("start worktree add job: %v", err)
	}

	snap, err := mgr.Wait(context.Background(), started.ID, 15*time.Second, 20, 0)
	if err != nil {
		t.Fatalf("wait worktree add job: %v", err)
	}
	if snap.Status != CommandJobCompleted {
		t.Fatalf("expected completed, got %s (err=%q)", snap.Status, snap.ErrText)
	}
	if snap.SafetyHint == "" {
		t.Fatal("#5278 regression: Wait-path snapshot has empty SafetyHint for a detached-HEAD worktree add - hint write lost the race against close(done)")
	}

	// The hint must stay stable afterwards (locked snapshot read).
	again, err := mgr.Read(started.ID, 20, 0)
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if again.SafetyHint != snap.SafetyHint {
		t.Fatalf("SafetyHint changed between Wait and Read: %q vs %q", snap.SafetyHint, again.SafetyHint)
	}
}

// Test B (#5278 race): hammer concurrent Read while jobs complete; under
// `go test -race` the pre-fix unlocked safetyHint write flags DATA RACE.
func TestIssue5278_SafetyHintNoRaceWithConcurrentRead(t *testing.T) {
	mgr := NewCommandJobManager(t.TempDir())
	for i := 0; i < 25; i++ {
		started, err := mgr.Start(context.Background(), "sleep 0.02", false, 10*time.Second)
		if err != nil {
			t.Fatalf("iter %d start: %v", i, err)
		}
		done := make(chan struct{})
		go func(id string) {
			for {
				if _, err := mgr.Read(id, 5, 0); err != nil {
					return // evicted/unknown - stop hammering
				}
				select {
				case <-done:
					return
				default:
				}
			}
		}(started.ID)
		snap, err := mgr.Wait(context.Background(), started.ID, 5*time.Second, 5, 0)
		close(done)
		<-done
		if err != nil {
			t.Fatalf("iter %d wait: %v", i, err)
		}
		if snap.Status != CommandJobCompleted {
			t.Fatalf("iter %d: expected completed, got %s", i, snap.Status)
		}
	}
}
