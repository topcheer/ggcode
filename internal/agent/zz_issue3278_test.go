package agent

// Probes for #3278: /traj clear only purged the workspace tier while the
// renderer kept injecting global-tier fillers - visible in /traj list
// but unkillable without a manual rm. The fix adds a global-tier clear
// (same lock discipline) plus a remaining-count helper that powers the
// bare-clear hint.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIssue3278_ClearGlobalAndRemaining(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	gp := filepath.Join(home, ".ggcode", "trajectory-learnings.jsonl")
	if err := os.MkdirAll(filepath.Dir(gp), 0o755); err != nil {
		t.Fatal(err)
	}
	writeLearnings(t, home, []trajectoryLearning{
		mkLearning(time.Now(), "strategy", "gcat", "global insight"),
		mkLearning(time.Now(), "recovery", "gcat2", "another"),
	})

	if n, err := TrajGlobalRemaining(); err != nil || n != 2 {
		t.Fatalf("remaining: n=%d err=%v (want 2)", n, err)
	}
	if err := TrajClearGlobalLearnings(); err != nil {
		t.Fatalf("clear global: %v", err)
	}
	if n, err := TrajGlobalRemaining(); err != nil || n != 0 {
		t.Fatalf("after clear: n=%d err=%v (want 0)", n, err)
	}
	// Idempotent: second clear on an absent file is fine.
	if err := TrajClearGlobalLearnings(); err != nil {
		t.Fatalf("clear global must be idempotent: %v", err)
	}
}

func TestIssue3278_RemainingZeroWhenGlobalAbsent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	n, err := TrajGlobalRemaining()
	if err != nil || n != 0 {
		t.Fatalf("absent global store: n=%d err=%v (want 0,nil - no hint for a clean tier)", n, err)
	}
}
