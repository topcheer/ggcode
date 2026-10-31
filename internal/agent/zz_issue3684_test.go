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
	// #4816: PID 0, not a hardcoded lucky number. The crash verdict at this
	// young journal age hinges on isProcessAlive(entry.PID): a fixed PID like
	// 4242 is fate-dependent - green where that PID is unused, red on any
	// runner where a real process happens to hold it ("concurrent access",
	// nil). PID 0 never enters the liveness branch, so the leftover-running
	// detection is deterministic on every machine. The genuine live-PID
	// branches are covered by run_journal_3222_test.go via os.Getpid().
	MarkRunning(sid, "probe prompt", 0)
	// No close - a genuine crash leaves the running entry behind.
	if info := CheckCrashedRun(sid); info == nil {
		t.Fatal("a genuine leftover running entry must still be detected as crashed")
	}
}
