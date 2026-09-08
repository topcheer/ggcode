package tui

import (
	"testing"
	"time"
)

// #1890: scheduled knight tasks run on their own agent and never touch
// loading/cancelFunc; knightRunning (counted here) is what keeps the
// remote-entry idle gates closed while they run. The START event must NOT
// clear loading (a scheduled task never set it - it would clear a state
// owned by something else).
func TestKnightTaskEventCountsRunning(t *testing.T) {
	m := newTestModel()
	if m.knightRunning != 0 {
		t.Fatalf("baseline knightRunning = %d, want 0", m.knightRunning)
	}

	// START event: count goes up, no loading interference.
	m2, _ := m.Update(knightTaskEventMsg{TaskName: "maintenance"})
	mm, ok := m2.(Model)
	if !ok {
		t.Fatalf("start event returned %T, want Model", m2)
	}
	if mm.knightRunning != 1 {
		t.Fatalf("after start, knightRunning = %d, want 1", mm.knightRunning)
	}

	// Completion event: count goes back down.
	m3, _ := mm.Update(knightTaskEventMsg{TaskName: "maintenance", Report: "done", Duration: time.Second})
	m3m, ok := m3.(Model)
	if !ok {
		t.Fatalf("completion event returned %T, want Model", m3)
	}
	if got := m3m.knightRunning; got != 0 {
		t.Fatalf("after completion, knightRunning = %d, want 0", got)
	}

	// A stray completion (no matching start) must not underflow below 0.
	m4, _ := m.Update(knightTaskEventMsg{TaskName: "ghost", Report: "late"})
	m4m, ok4 := m4.(Model)
	if !ok4 || m4m.knightRunning != 0 {
		t.Fatalf("stray completion: got %T, want Model with knightRunning=0", m4)
	}
}
