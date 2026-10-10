package agent

import (
	"encoding/json"
	"testing"
)

// #3840: start_command's result is the LAUNCH outcome, not the test
// outcome. A successful launch must not clear lastBuildFailed nor satisfy
// the final-turn evidence gate; the background job's TERMINAL outcome
// (observed via read_command_output/wait_command) is the real evidence.

func TestIssue3840_LaunchDoesNotFakeTestEvidence(t *testing.T) {
	a := &Agent{}
	a.postEditVerify.lastBuildFailed = true // a previous foreground test failed
	args := json.RawMessage(`{"command":"go test ./..."}`)
	// background launch succeeds (spawn ok) - the test itself later FAILS
	a.maybeResetVerifyOnCommand("start_command", args, false)
	if a.postEditVerify.realBuildOrTestRunThisRun {
		t.Fatal("#3840: successful launch must not count as real test evidence")
	}
	if !a.postEditVerify.lastBuildFailed {
		t.Fatal("#3840: successful launch must not clear the prior failure flag")
	}
	if !a.postEditVerify.buildOrTestRunThisRun || a.postEditVerify.sourceEditsSinceHint != 0 {
		t.Fatal("launch still resets the soft counters")
	}
}

func TestIssue3840_TerminalBackfillAppliesRealOutcome(t *testing.T) {
	a := &Agent{}
	// failed background test: failure flag re-armed, still real evidence of a run
	a.recordBgVerifyOutcome("go test ./...", false)
	if !a.postEditVerify.lastBuildFailed {
		t.Fatal("terminal FAIL must set lastBuildFailed (re-arm the '(which FAILED)' hint)")
	}
	if !a.postEditVerify.realBuildOrTestRunThisRun {
		t.Fatal("terminal FAIL is still real evidence a test ran")
	}
	// passed background test: green evidence
	a2 := &Agent{}
	a2.recordBgVerifyOutcome("go test ./...", true)
	if a2.postEditVerify.lastBuildFailed {
		t.Fatal("terminal PASS must clear the failure flag")
	}
	if !a2.postEditVerify.realBuildOrTestRunThisRun {
		t.Fatal("terminal PASS must satisfy the final-turn gate's evidence demand")
	}
}

func TestIssue3840_NonTestBackfillIgnored(t *testing.T) {
	a := &Agent{}
	// Non-test command terminal outcome must not touch the verify state
	// (mirrors the #1841/#3045 gating of the foreground path).
	a.recordBgVerifyOutcome("gofmt -l .", true)
	if a.postEditVerify.realBuildOrTestRunThisRun {
		t.Fatal("non-test command must not set realBuildOrTestRunThisRun")
	}
}
