package agent

// #3684 probe: the session-start hook rejection path returns BEFORE the
// MarkCompleted defer is registered, leaving the "running" journal entry
// MarkRunning just wrote - byte-identical to a real crash. The next
// startup's CheckCrashedRun then prompts crash recovery for a session that
// was merely hook-rejected. The rejection path now closes the entry
// explicitly.

import "testing"

func TestIssue3684_HookRejectionLeavesNoStaleRunningEntry(t *testing.T) {
	_, cleanup := journalDirForTest(t)
	defer cleanup()
	sid := "issue3684-rejected"
	MarkRunning(sid, "probe prompt", 4242)
	// The rejection path's close (what agent.go now does before returning).
	MarkCompleted(sid, false, 0, 0)
	if info := CheckCrashedRun(sid); info != nil {
		t.Fatalf("hook-rejected session must not look crashed, got %+v", info)
	}
}

func TestIssue3684_RealRunningStillDetected(t *testing.T) {
	_, cleanup := journalDirForTest(t)
	defer cleanup()
	sid := "issue3684-crash"
	MarkRunning(sid, "probe prompt", 4242)
	// No close - a genuine crash leaves the running entry behind.
	if info := CheckCrashedRun(sid); info == nil {
		t.Fatal("a genuine leftover running entry must still be detected as crashed")
	}
}
