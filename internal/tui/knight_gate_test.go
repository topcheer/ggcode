package tui

import (
	"testing"
	"time"
)

func TestKnightTaskEventCountsRunning(t *testing.T) {
	m := newTestModel()
	if m.knightRunning != 0 {
		t.Fatalf("baseline knightRunning = %d, want 0", m.knightRunning)
	}

	m2, _ := m.Update(knightTaskEventMsg{TaskName: "maintenance"})
	mm, ok := m2.(Model)
	if !ok {
		t.Fatalf("start event returned %T, want Model", m2)
	}
	if mm.knightRunning != 1 {
		t.Fatalf("after start, knightRunning = %d, want 1", mm.knightRunning)
	}

	m3, _ := mm.Update(knightTaskEventMsg{TaskName: "maintenance", Report: "done", Duration: time.Second})
	m3m, ok := m3.(Model)
	if !ok {
		t.Fatalf("completion event returned %T, want Model", m3)
	}
	if got := m3m.knightRunning; got != 0 {
		t.Fatalf("after completion, knightRunning = %d, want 0", got)
	}

	m4, _ := m.Update(knightTaskEventMsg{TaskName: "ghost", Report: "late"})
	m4m, ok4 := m4.(Model)
	if !ok4 || m4m.knightRunning != 0 {
		t.Fatalf("stray completion: got %T, want Model with knightRunning=0", m4)
	}
}
