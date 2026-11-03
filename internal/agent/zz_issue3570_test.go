package agent

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

	q.markUndelivered()
	if q.warned || q.warnCount != 0 {
		t.Fatalf("rollback must restore (false,0), got (%v,%d)", q.warned, q.warnCount)
	}

	if msg2 := q.maybeWarn(5); msg2 == "" {
		t.Fatal("rolled-back warn must be re-deliverable")
	}
}
