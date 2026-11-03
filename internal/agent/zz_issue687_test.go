package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newIssue687Monorepo(t *testing.T) (*monorepoScoperState, string) {
	t.Helper()
	root := t.TempDir()

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

	for i := 0; i < maxMonorepoSuppressions; i++ {
		s.markUndelivered()
		if msg := s.maybeWarnScopeSprawl(); msg == "" {
			t.Fatalf("iteration %d: re-armed hint must fire again below the cap", i)
		}
	}

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
