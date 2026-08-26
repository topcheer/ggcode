package a2a

import "testing"

// TestIssue1107_AuthRequiredNotTerminal guards #1107: auth-required is a
// pseudo-terminal state per the A2A spec. Listing it as terminal froze the
// task forever (done closed, transitions blocked, cancel/continue refused).
func TestIssue1107_AuthRequiredNotTerminal(t *testing.T) {
	if TaskStateAuthRequired.IsTerminal() {
		t.Fatal("auth-required must not be terminal (#1107): tasks in this state must stay resumable")
	}
	// Input-required is the established pseudo-terminal reference case.
	if TaskStateInputRequired.IsTerminal() {
		t.Fatal("input-required must not be terminal")
	}
	// True terminal states stay terminal.
	for _, s := range []TaskState{TaskStateCompleted, TaskStateFailed, TaskStateCanceled, TaskStateRejected} {
		if !s.IsTerminal() {
			t.Fatalf("state %s must remain terminal", s)
		}
	}
}
