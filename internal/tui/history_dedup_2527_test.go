package tui

import "testing"

// #2527: pushHistory must dedup exact matches by promoting the existing
// entry to the most-recent slot instead of appending a duplicate.
func TestPushHistoryDuplicatePromotesToFront(t *testing.T) {
	m := newTestModel()
	m.history = []string{"alpha", "beta", "gamma"}

	// Resubmit "beta": no duplicate, beta moves to the most-recent slot.
	m.pushHistory("beta")

	want := []string{"alpha", "gamma", "beta"}
	if len(m.history) != len(want) {
		t.Fatalf("history length = %d, want %d (no dup append)", len(m.history), len(want))
	}
	for i := range want {
		if m.history[i] != want[i] {
			t.Fatalf("history[%d] = %q, want %q (full: %v)", i, m.history[i], want[i], m.history)
		}
	}
	if m.historyIdx != len(m.history) {
		t.Fatalf("historyIdx = %d, want %d", m.historyIdx, len(m.history))
	}
}

func TestPushHistoryNonDuplicateAppends(t *testing.T) {
	m := newTestModel()
	m.history = []string{"alpha", "beta"}

	m.pushHistory("delta")

	want := []string{"alpha", "beta", "delta"}
	if len(m.history) != len(want) {
		t.Fatalf("history length = %d, want %d", len(m.history), len(want))
	}
	for i := range want {
		if m.history[i] != want[i] {
			t.Fatalf("history[%d] = %q, want %q", i, m.history[i], want[i])
		}
	}
}

func TestPushHistoryCJKExactMatchDedup(t *testing.T) {
	m := newTestModel()
	m.history = []string{"list files", "fix the bug", "list files"}

	// Exact CJK resubmit: only one occurrence survives, at the most-recent slot.
	m.pushHistory("fix the bug")

	want := []string{"list files", "list files", "fix the bug"}
	if len(m.history) != len(want) {
		t.Fatalf("history length = %d, want %d", len(m.history), len(want))
	}
	if m.history[len(m.history)-1] != "fix the bug" {
		t.Fatalf("most recent = %q, want %q", m.history[len(m.history)-1], "fix the bug")
	}
	// Both pre-existing occurrences must be exact matches for the dedup to
	// remove only one; the other "list files" must survive untouched.
	if m.history[0] != "list files" || m.history[1] != "list files" {
		t.Fatalf("unrelated entries changed: %v", m.history)
	}
}

func TestPushHistoryEmptyHistory(t *testing.T) {
	m := newTestModel()
	m.history = nil

	m.pushHistory("first")

	if len(m.history) != 1 || m.history[0] != "first" {
		t.Fatalf("history = %v, want [first]", m.history)
	}
	if m.historyIdx != 1 {
		t.Fatalf("historyIdx = %d, want 1", m.historyIdx)
	}
}
