package agent

import "testing"

func TestIssue3684_HookRejectionLeavesNoStaleRunningEntry(t *testing.T) {
	_, cleanup := journalDirForTest(t)
	defer cleanup()
	sid := "issue3684-rejected"
	MarkRunning(sid, "probe prompt", 4242)

	MarkCompleted(sid, false, 0, 0)
	if info := CheckCrashedRun(sid); info != nil {
		t.Fatalf("hook-rejected session must not look crashed, got %+v", info)
	}
}

func TestIssue3684_RealRunningStillDetected(t *testing.T) {
	_, cleanup := journalDirForTest(t)
	defer cleanup()
	sid := "issue3684-crash"

	MarkRunning(sid, "probe prompt", 0)

	if info := CheckCrashedRun(sid); info == nil {
		t.Fatal("a genuine leftover running entry must still be detected as crashed")
	}
}
