package agent

// #3570 probe (companion to TestQueryConvergeWarnCap's cap-2 encoding):
// the undelivered-rollback channel must keep working when the quota is 2 -
// a warn that never reached the model rolls the count back and stays
// re-deliverable.

import "testing"

func TestIssue3570_UndeliveredRollbackReArms(t *testing.T) {
	q := newQueryConvergeState()
	q.recordToolCall("grep", `{"pattern":"authentication handler login"}`, 1)
	q.recordToolCall("grep", `{"pattern":"auth handler login function"}`, 2)
	q.recordToolCall("grep", `{"pattern":"authentication login handler"}`, 3)
	if msg1 := q.maybeWarn(4); msg1 == "" {
		t.Fatal("expected first warning")
	}
	if !q.warned {
		t.Fatal("warned observability latch must be set on fire (#3380)")
	}
	// Delivery failed: rollback both the count and the latch.
	q.markUndelivered()
	if q.warned || q.warnCount != 0 {
		t.Fatalf("rollback must restore (false,0), got (%v,%d)", q.warned, q.warnCount)
	}
	// Re-deliverable: the same convergence fires again.
	if msg2 := q.maybeWarn(5); msg2 == "" {
		t.Fatal("rolled-back warn must be re-deliverable")
	}
}
