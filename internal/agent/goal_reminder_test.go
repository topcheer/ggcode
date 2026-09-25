package agent

import (
	"strings"
	"testing"
)

// Unit tests for the goal fade-out reminder (OpenDev arXiv:2603.05344
// event-driven system reminders). Pure cadence - no detection involved.

func TestGoalReminderDisabledWithoutGoal(t *testing.T) {
	s := newGoalReminderState()
	for _, iter := range []int{1, 5, 12, 50, 200} {
		if msg := s.maybeRemind(iter, ""); msg != "" {
			t.Fatalf("iteration %d: empty goal must disable reminders, got %q", iter, msg)
		}
	}
}

func TestGoalReminderNotBeforeFirstIter(t *testing.T) {
	s := newGoalReminderState()
	for iter := 1; iter < goalReminderFirstIter; iter++ {
		if msg := s.maybeRemind(iter, "ship the feature"); msg != "" {
			t.Fatalf("iteration %d: reminder must not fire before iteration %d, got %q", iter, goalReminderFirstIter, msg)
		}
	}
	if msg := s.maybeRemind(goalReminderFirstIter, "ship the feature"); msg == "" {
		t.Fatal("reminder should fire exactly at goalReminderFirstIter")
	}
}

func TestGoalReminderContentAnchorsGoal(t *testing.T) {
	s := newGoalReminderState()
	msg := s.maybeRemind(goalReminderFirstIter, "make verify-ci green")
	if !strings.Contains(msg, "[goal-reminder]") {
		t.Errorf("reminder must carry the [goal-reminder] tag, got %q", msg)
	}
	if !strings.Contains(msg, "make verify-ci green") {
		t.Errorf("reminder must quote the active goal, got %q", msg)
	}
}

func TestGoalReminderCadence(t *testing.T) {
	s := newGoalReminderState()
	if msg := s.maybeRemind(12, "g"); msg == "" {
		t.Fatal("first reminder should fire at iteration 12")
	}
	// Within the cadence window: silent.
	for iter := 13; iter < 12+goalReminderEveryIter; iter++ {
		if msg := s.maybeRemind(iter, "g"); msg != "" {
			t.Fatalf("iteration %d: inside cadence window, got %q", iter, msg)
		}
	}
	// Boundary: due again exactly goalReminderEveryIter later.
	if msg := s.maybeRemind(12+goalReminderEveryIter, "g"); msg == "" {
		t.Fatalf("iteration %d: reminder due again", 12+goalReminderEveryIter)
	}
}

func TestGoalReminderMaxPerRun(t *testing.T) {
	s := newGoalReminderState()
	fired := 0
	for iter := 1; iter <= 500 && fired <= goalReminderMaxPerRun; iter++ {
		if msg := s.maybeRemind(iter, "g"); msg != "" {
			fired++
		}
	}
	if fired != goalReminderMaxPerRun {
		t.Fatalf("expected exactly %d reminders per run, got %d", goalReminderMaxPerRun, fired)
	}
	if msg := s.maybeRemind(1000, "g"); msg != "" {
		t.Fatalf("quota exhausted: got %q", msg)
	}
}

func TestGoalReminderUndeliveredRetry(t *testing.T) {
	s := newGoalReminderState()
	// Slot consumed but delivery suppressed by the guidance budget.
	if msg := s.maybeRemind(12, "g"); msg == "" {
		t.Fatal("setup: reminder should fire")
	}
	s.markUndelivered(12)
	// Next iteration must be due again (no lost reminder).
	if msg := s.maybeRemind(13, "g"); msg == "" {
		t.Fatal("suppressed reminder should retry on the next iteration")
	}
}

func TestGoalReminderUndeliveredDoesNotRefundWrongSlot(t *testing.T) {
	s := newGoalReminderState()
	if msg := s.maybeRemind(12, "g"); msg == "" {
		t.Fatal("setup: first reminder should fire")
	}
	// A stale refund call (wrong iteration) must not rewind the cadence.
	s.markUndelivered(11)
	if msg := s.maybeRemind(13, "g"); msg != "" {
		t.Fatalf("delivered reminder at 12 must keep its slot, got %q", msg)
	}
	// And the quota must still count the delivered reminder.
	if msg := s.maybeRemind(27, "g"); msg == "" {
		t.Fatal("second reminder at 27 should be due (12+15)")
	}
}

func TestGoalReminderReset(t *testing.T) {
	s := newGoalReminderState()
	if msg := s.maybeRemind(12, "g"); msg == "" {
		t.Fatal("setup: reminder should fire")
	}
	s.reset()
	if s.lastFiredIter != 0 || s.delivered != 0 {
		t.Fatalf("reset must zero cadence state, got lastFired=%d delivered=%d", s.lastFiredIter, s.delivered)
	}
	// After reset the cadence restarts from the first-iteration gate.
	if msg := s.maybeRemind(5, "g"); msg != "" {
		t.Fatalf("post-reset reminder before goalReminderFirstIter, got %q", msg)
	}
	if msg := s.maybeRemind(12, "g"); msg == "" {
		t.Fatal("post-reset reminder should fire again at 12")
	}
}

func TestGoalReminderNilSafe(t *testing.T) {
	var s *goalReminderState
	if msg := s.maybeRemind(50, "g"); msg != "" {
		t.Fatalf("nil receiver must be inert, got %q", msg)
	}
	s.markUndelivered(50) // must not panic
	s.reset()             // must not panic
}
