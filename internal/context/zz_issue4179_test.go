package context

import (
	"context"
	"testing"
)

func TestSummarizeNoPlanResetsAppliedFlag(t *testing.T) {
	m := newTestManager(t)

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

	changed, err := m.CheckAndSummarize(context.Background(), nil)
	if err != nil {
		t.Fatalf("CheckAndSummarize: %v", err)
	}
	if changed {
		t.Fatal("CheckAndSummarize must return changed=false when nothing was applied")
	}
}

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
