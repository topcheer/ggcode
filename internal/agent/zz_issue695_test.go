package agent

// Regression test for issue #695 (follow-up to #687):
// maybeWarnScopeSprawl's `suppressions > maxMonorepoSuppressions` clause was
// unreachable dead code — suppressions can never exceed the cap because
// markUndelivered early-returns at >= cap. The guard was removed; termination
// is guaranteed by `fired` alone. This test pins the actual budget so future
// edits do not silently change it: 1 initial fire + 3 re-arms = 4 attempts
// per run, with reset() restoring both counters.

import (
	"strings"
	"testing"
)

func TestIssue695_SuppressionBudget_FourAttemptsPerRun(t *testing.T) {
	s := &monorepoScoperState{
		enabled:     true,
		touchedDirs: map[string]int{"users": 1, "orders": 1, "billing": 1},
	}

	fires := 0
	for attempt := 0; attempt < 10; attempt++ {
		if hint := s.maybeWarnScopeSprawl(); hint != "" {
			fires++
			if !strings.Contains(hint, "monorepo-scope") {
				t.Fatalf("hint malformed: %s", hint)
			}
		}
		// Simulate the guidance budget bouncing the hint each time.
		s.markUndelivered()
	}
	if fires != 1+maxMonorepoSuppressions {
		t.Fatalf("budget must allow exactly 1+%d attempts per run, got %d", maxMonorepoSuppressions, fires)
	}
}

func TestIssue695_SuppressionsNeverExceedCap(t *testing.T) {
	s := &monorepoScoperState{
		enabled:     true,
		touchedDirs: map[string]int{"users": 1, "orders": 1, "billing": 1},
	}
	for i := 0; i < 10; i++ {
		s.maybeWarnScopeSprawl()
		s.markUndelivered()
	}
	if s.suppressions != maxMonorepoSuppressions {
		t.Fatalf("suppressions must cap at %d, got %d", maxMonorepoSuppressions, s.suppressions)
	}
	if !s.fired {
		t.Fatal("fired must stay true once the re-arm budget is exhausted — it is the only termination guard (#695)")
	}
	// Exhausted budget: no further fires even though the hint would qualify.
	if hint := s.maybeWarnScopeSprawl(); hint != "" {
		t.Fatalf("expected no further fires after budget exhaustion, got %s", hint)
	}
}

func TestIssue695_ResetRestoresBudget(t *testing.T) {
	s := &monorepoScoperState{
		enabled:     true,
		touchedDirs: map[string]int{"users": 1, "orders": 1, "billing": 1},
	}
	s.maybeWarnScopeSprawl()
	s.markUndelivered()
	s.maybeWarnScopeSprawl()
	s.markUndelivered()

	s.reset()
	if s.fired || s.suppressions != 0 {
		t.Fatalf("reset must clear fired and suppressions, got fired=%v suppressions=%d", s.fired, s.suppressions)
	}
	// touchedDirs is per-run too; the next run re-records edits before the
	// hint can qualify again.
	s.recordEdit("users/index.ts")
	s.recordEdit("orders/index.ts")
	s.recordEdit("billing/index.ts")
	if hint := s.maybeWarnScopeSprawl(); hint == "" {
		t.Fatal("after reset the hint must be able to fire again in the next run")
	}
}
