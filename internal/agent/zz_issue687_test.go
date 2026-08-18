package agent

// Regression test for issue #687 (regression of #681): the monorepo hint's
// budget-suppression retry was unbounded — during a permanently saturated
// turn (e.g. errorCompound storms always claiming the budget first) the
// detector re-paid the O(touchedDirs) check every iteration and was
// starved forever. The re-arm is now capped at maxMonorepoSuppressions.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newIssue687Monorepo(t *testing.T) (*monorepoScoperState, string) {
	t.Helper()
	root := t.TempDir()
	// 3+ subdirs with package manifests → fallback monorepo detection.
	for _, d := range []string{"svc-a", "svc-b", "svc-c"} {
		sub := filepath.Join(root, d)
		if err := os.MkdirAll(sub, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "go.mod"), []byte("module x\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	s := newMonorepoScoperState()
	s.detectMonorepo(root)
	return s, root
}

func TestIssue687_MonorepoRetryBounded(t *testing.T) {
	s, root := newIssue687Monorepo(t)
	s.recordEdit(filepath.Join(root, "svc-a", "a.go"))
	s.recordEdit(filepath.Join(root, "svc-b", "b.go"))
	s.recordEdit(filepath.Join(root, "svc-c", "c.go"))

	if msg := s.maybeWarnScopeSprawl(); msg == "" {
		t.Fatal("expected sprawl warning on first check")
	}

	// Simulate budget rejection maxMonorepoSuppressions+1 times: each
	// rejection re-arms once, but the loop must terminate.
	for i := 0; i < maxMonorepoSuppressions; i++ {
		s.markUndelivered()
		if msg := s.maybeWarnScopeSprawl(); msg == "" {
			t.Fatalf("iteration %d: re-armed hint must fire again below the cap", i)
		}
	}
	// At the cap, one more rejection permanently disarms — no infinite retry.
	s.markUndelivered()
	if msg := s.maybeWarnScopeSprawl(); msg != "" {
		t.Fatal("hint must stop retrying after maxMonorepoSuppressions rejections")
	}
}

func TestIssue687_MonorepoRetryCounterNoLeakAcrossRuns(t *testing.T) {
	s, root := newIssue687Monorepo(t)
	s.recordEdit(filepath.Join(root, "svc-a", "a.go"))
	s.recordEdit(filepath.Join(root, "svc-b", "b.go"))
	s.recordEdit(filepath.Join(root, "svc-c", "c.go"))

	if msg := s.maybeWarnScopeSprawl(); msg == "" || !strings.Contains(msg, "svc") {
		t.Fatalf("expected sprawl warning, got %q", msg)
	}
	s.markUndelivered()
	// Reset for the next run: both the one-shot and the suppression budget
	// must clear (per-run-2 semantics). Re-record edits (reset clears them).
	s.reset()
	s.recordEdit(filepath.Join(root, "svc-a", "a.go"))
	s.recordEdit(filepath.Join(root, "svc-b", "b.go"))
	s.recordEdit(filepath.Join(root, "svc-c", "c.go"))
	if s.fired || s.suppressions != 0 {
		t.Fatalf("reset must clear fired and suppressions, got fired=%v suppressions=%d", s.fired, s.suppressions)
	}
	if msg := s.maybeWarnScopeSprawl(); msg == "" {
		t.Fatal("hint must fire fresh on the next run")
	}
}
