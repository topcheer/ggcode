package agent

// #3380 probes: quota-bearing detectors must not burn their per-run quota
// when the guidance budget suppresses delivery - markUndelivered restores
// the pre-fire state so the warning re-fires on a later iteration.

import "testing"

func TestIssue3380_MarkUndeliveredRestoresQuota(t *testing.T) {
	// queryConverge: 1/run quota, warned flag.
	q := &queryConvergeState{}
	q.mu.Lock()
	q.warned = true
	q.warnCount = 1
	q.mu.Unlock()
	q.markUndelivered()
	q.mu.Lock()
	warned, count := q.warned, q.warnCount
	q.mu.Unlock()
	if warned || count != 0 {
		t.Fatalf("queryConverge rollback failed: warned=%v warnCount=%d", warned, count)
	}

	// reasoningRedund: 2/run quota.
	r := &reasoningRedundancyState{warnCount: 1, totalFire: 1}
	r.markUndelivered()
	if r.warnCount != 0 || r.totalFire != 0 {
		t.Fatalf("reasoningRedund rollback failed: %d/%d", r.warnCount, r.totalFire)
	}

	// bareEditStreak: quota + spacing marker restore.
	b := &bareEditStreakState{warnCount: 1, lastWarnedAt: 7, prevLastWarnedAt: 3, canRevert: true}
	b.markUndelivered()
	if b.warnCount != 0 || b.lastWarnedAt != 3 {
		t.Fatalf("bareEditStreak rollback failed: warnCount=%d lastWarnedAt=%d", b.warnCount, b.lastWarnedAt)
	}

	// futileCycle: quota + epoch marker restore.
	f := &futileCycleState{warningsFired: 1, lastWarnedEpoch: 4, prevWarnedEpoch: 2, canRevert: true}
	f.markUndelivered()
	if f.warningsFired != 0 || f.lastWarnedEpoch != 2 {
		t.Fatalf("futileCycle rollback failed: fired=%d epoch=%d", f.warningsFired, f.lastWarnedEpoch)
	}
}

func TestIssue3380_MarkUndeliveredNoRevertibleFire(t *testing.T) {
	// Without a fire to revert, the rollback must be a safe no-op.
	b := &bareEditStreakState{}
	b.markUndelivered()
	if b.warnCount != 0 || b.lastWarnedAt != 0 {
		t.Fatal("no-op rollback must not mutate state")
	}
	f := &futileCycleState{}
	f.markUndelivered()
	if f.warningsFired != 0 {
		t.Fatal("no-op rollback must not mutate state")
	}
	// Double rollback: the second call must not underflow or double-restore.
	q := &queryConvergeState{}
	q.markUndelivered()
	q.markUndelivered()
}
