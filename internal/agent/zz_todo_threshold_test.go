package agent

import "testing"

func TestStaleThresholdForMatrix(t *testing.T) {
	if got := staleThresholdFor(true); got != 5 {
		t.Fatalf("active goal (in_progress present) must use 5, got %d", got)
	}
	if got := staleThresholdFor(false); got != 8 {
		t.Fatalf("pending-only must keep 8, got %d", got)
	}
}

func TestShouldRemindRespectsActiveGoalThreshold(t *testing.T) {
	s := newTodoStalenessState()
	s.hasTodos = true
	s.recordUpdate(10, 3)

	s.activeGoal = true
	if !s.shouldRemind(15, true) {
		t.Fatal("in_progress list at iter 15 (5 since update) should remind")
	}
	s2 := newTodoStalenessState()
	s2.hasTodos = true
	s2.recordUpdate(10, 3)
	s2.activeGoal = false
	if s2.shouldRemind(15, true) {
		t.Fatal("pending-only list at 5-since-update must NOT remind yet (threshold 8)")
	}
	if !s2.shouldRemind(18, true) {
		t.Fatal("pending-only list at 8-since-update must remind")
	}
}
