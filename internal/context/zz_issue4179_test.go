package context

import (
	"context"
	"testing"
)

// TestSummarizeNoPlanResetsAppliedFlag (#4179): the no-plan early-exit path
// of Summarize must reset lastSummarizeApplied. Without the reset, a stale
// `true` from a previous successful Summarize leaks into CheckAndSummarize,
// which reports changed=true and the caller skips its fallbacks (PTL
// truncate retry) while the context is still over budget: the exact
// failure mode #702b fixed for the TOCTOU-discard path.
func TestSummarizeNoPlanResetsAppliedFlag(t *testing.T) {
	m := newTestManager(t)

	// Simulate a previous successful Summarize: the flag is latched true
	// at manager.go L1518. An empty manager cannot build a summary plan
	// (buildSummaryPlan returns !ok with no messages), which is precisely
	// the "second call finds nothing compressible" trigger of #4179.
	m.mu.Lock()
	m.lastSummarizeApplied = true
	m.mu.Unlock()

	if err := m.Summarize(context.Background(), nil); err != nil {
		t.Fatalf("Summarize on no-plan path: %v", err)
	}

	m.mu.Lock()
	applied := m.lastSummarizeApplied
	m.mu.Unlock()
	if applied {
		t.Fatal("no-plan early-exit must reset lastSummarizeApplied; stale true makes CheckAndSummarize misreport changed=true")
	}

	// End-to-end: CheckAndSummarize must report changed=false so callers
	// run their fallback compaction paths.
	changed, err := m.CheckAndSummarize(context.Background(), nil)
	if err != nil {
		t.Fatalf("CheckAndSummarize: %v", err)
	}
	if changed {
		t.Fatal("CheckAndSummarize must return changed=false when nothing was applied")
	}
}

// TestClearResetsSummarizeAppliedFlag (#4179, defensive): a user-driven
// reset must not let a stale applied-flag from the pre-clear session leak
// into the next session's CheckAndSummarize.
func TestClearResetsSummarizeAppliedFlag(t *testing.T) {
	m := newTestManager(t)

	m.mu.Lock()
	m.lastSummarizeApplied = true
	m.mu.Unlock()

	m.Clear()

	m.mu.Lock()
	applied := m.lastSummarizeApplied
	m.mu.Unlock()
	if applied {
		t.Fatal("Clear must reset lastSummarizeApplied")
	}
}
