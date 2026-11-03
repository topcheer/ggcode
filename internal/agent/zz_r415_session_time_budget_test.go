package agent

import (
	"strings"
	"testing"
	"time"
)

func TestR415_TimeBudgetLadder(t *testing.T) {
	s := &sessionTimeBudgetState{budget: 10 * time.Minute}

	msg, stop := s.checkLocked(5 * time.Minute)
	if msg != "" || stop {
		t.Fatalf("50%%: got msg=%q stop=%v, want silent", msg, stop)
	}

	msg, stop = s.checkLocked(8 * time.Minute)
	if msg == "" || stop || !strings.Contains(msg, "80%") {
		t.Fatalf("80%%: msg=%q stop=%v", msg, stop)
	}
	msg, stop = s.checkLocked(8 * time.Minute)
	if msg != "" {
		t.Fatal("80% fired twice")
	}

	msg, stop = s.checkLocked(9*time.Minute + 30*time.Second)
	if msg == "" || stop || !strings.Contains(msg, "95%") {
		t.Fatalf("95%%: msg=%q stop=%v", msg, stop)
	}
	msg, _ = s.checkLocked(9*time.Minute + 35*time.Second)
	if msg != "" {
		t.Fatal("95% fired twice")
	}

	msg, stop = s.checkLocked(10 * time.Minute)
	if msg == "" || !stop || !strings.Contains(msg, "exhausted") {
		t.Fatalf("100%%: msg=%q stop=%v", msg, stop)
	}
	msg, stop = s.checkLocked(11 * time.Minute)
	if msg != "" || stop {
		t.Fatal("stop fired twice")
	}
}

func TestR415_TimeBudgetJumpPastStop(t *testing.T) {
	s := &sessionTimeBudgetState{budget: time.Minute}
	msg, stop := s.checkLocked(2 * time.Minute)
	if msg == "" || stop || !strings.Contains(msg, "95%") {
		t.Fatalf("jump: msg=%q stop=%v, want 95%% first", msg, stop)
	}
	msg, stop = s.checkLocked(2 * time.Minute)
	if msg == "" || !stop {
		t.Fatalf("second crossing: msg=%q stop=%v, want stop", msg, stop)
	}
}

func TestR415_TimeBudgetUnset(t *testing.T) {
	s := &sessionTimeBudgetState{}
	if msg, stop := s.checkLocked(time.Hour); msg != "" || stop {
		t.Fatalf("unset: msg=%q stop=%v", msg, stop)
	}
}

func TestR415_TimeBudgetSetGetClear(t *testing.T) {
	a := &Agent{}
	if got := a.SessionTimeBudget(); got != 0 {
		t.Fatalf("default = %v, want 0", got)
	}
	a.SetSessionTimeBudget(15 * time.Minute)
	if got := a.SessionTimeBudget(); got != 15*time.Minute {
		t.Fatalf("set = %v, want 15m", got)
	}
	a.SetSessionTimeBudget(0)
	if got := a.SessionTimeBudget(); got != 0 {
		t.Fatalf("clear = %v, want 0", got)
	}
}

func TestR415_TimeBudgetRecordLifecycle(t *testing.T) {
	a := &Agent{}

	a.resetSessionTimeUsage()
	if msg, stop := a.RecordSessionTimeUsage(); msg != "" || stop {
		t.Fatalf("no-budget record: msg=%q stop=%v", msg, stop)
	}

	a.SetSessionTimeBudget(time.Nanosecond)
	a.resetSessionTimeUsage()
	time.Sleep(time.Millisecond)

	msg, stop := a.RecordSessionTimeUsage()
	if msg == "" || stop {
		t.Fatalf("armed record: msg=%q stop=%v, want 95%% guidance", msg, stop)
	}
	msg, stop = a.RecordSessionTimeUsage()
	if msg == "" || !stop {
		t.Fatalf("follow-up record: msg=%q stop=%v, want stop", msg, stop)
	}

	a.resetSessionTimeUsage()
	s := sessionTimeBudgetStateFor(a)
	if s.warn95Given || s.stopGiven || s.runStart.IsZero() {
		t.Fatal("reset did not clear latches / re-arm runStart")
	}
}
