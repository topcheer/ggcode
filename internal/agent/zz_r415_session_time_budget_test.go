package agent

// r415 probes: session time budget soft ladder (80/95/100), mirroring the
// token budget's contract but for wall-clock time.

import (
	"strings"
	"testing"
	"time"
)

// The ladder itself, exercised directly with synthetic elapsed values.
func TestR415_TimeBudgetLadder(t *testing.T) {
	s := &sessionTimeBudgetState{budget: 10 * time.Minute}

	// 50%: silent.
	msg, stop := s.checkLocked(5 * time.Minute)
	if msg != "" || stop {
		t.Fatalf("50%%: got msg=%q stop=%v, want silent", msg, stop)
	}

	// 80%: convergence guidance, once.
	msg, stop = s.checkLocked(8 * time.Minute)
	if msg == "" || stop || !strings.Contains(msg, "80%") {
		t.Fatalf("80%%: msg=%q stop=%v", msg, stop)
	}
	msg, stop = s.checkLocked(8 * time.Minute)
	if msg != "" {
		t.Fatal("80% fired twice")
	}

	// 95%: finalize pressure, once.
	msg, stop = s.checkLocked(9*time.Minute + 30*time.Second)
	if msg == "" || stop || !strings.Contains(msg, "95%") {
		t.Fatalf("95%%: msg=%q stop=%v", msg, stop)
	}
	msg, _ = s.checkLocked(9*time.Minute + 35*time.Second)
	if msg != "" {
		t.Fatal("95% fired twice")
	}

	// 100%: wind-down stop, once.
	msg, stop = s.checkLocked(10 * time.Minute)
	if msg == "" || !stop || !strings.Contains(msg, "exhausted") {
		t.Fatalf("100%%: msg=%q stop=%v", msg, stop)
	}
	msg, stop = s.checkLocked(11 * time.Minute)
	if msg != "" || stop {
		t.Fatal("stop fired twice")
	}
}

// Jump straight past 100% from silence: the urgent guidance fires first
// (ordering rationale mirrors the token ladder, #543), stop on the next.
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

// Budget unset: permanently silent.
func TestR415_TimeBudgetUnset(t *testing.T) {
	s := &sessionTimeBudgetState{}
	if msg, stop := s.checkLocked(time.Hour); msg != "" || stop {
		t.Fatalf("unset: msg=%q stop=%v", msg, stop)
	}
}

// Setter/getter/clear via a real Agent.
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

// RecordSessionTimeUsage: defensive zero-runStart path (first call arms,
// stays silent), then a reset arms properly and a long-since-armed state
// reflects real elapsed time.
func TestR415_TimeBudgetRecordLifecycle(t *testing.T) {
	a := &Agent{}
	// No budget configured: Record is a no-op even after reset.
	a.resetSessionTimeUsage()
	if msg, stop := a.RecordSessionTimeUsage(); msg != "" || stop {
		t.Fatalf("no-budget record: msg=%q stop=%v", msg, stop)
	}

	// Budget shorter than test tolerance: after reset + first silent arm,
	// wait a tick so elapsed >= budget fires the 95% (jump ordering).
	a.SetSessionTimeBudget(time.Nanosecond)
	a.resetSessionTimeUsage()
	// First record after reset: elapsed ~= 0, budget 1ns -> jumps past
	// thresholds; urgent (95%) fires first by design, stop on the next.
	msg, stop := a.RecordSessionTimeUsage()
	if msg == "" || stop {
		t.Fatalf("armed record: msg=%q stop=%v, want 95%% guidance", msg, stop)
	}
	msg, stop = a.RecordSessionTimeUsage()
	if msg == "" || !stop {
		t.Fatalf("follow-up record: msg=%q stop=%v, want stop", msg, stop)
	}

	// Reset clears latches for the next run.
	a.resetSessionTimeUsage()
	s := sessionTimeBudgetStateFor(a)
	if s.warn95Given || s.stopGiven || s.runStart.IsZero() {
		t.Fatal("reset did not clear latches / re-arm runStart")
	}
}
