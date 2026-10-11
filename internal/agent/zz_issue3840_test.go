package agent

// #3840 probes: a start_command LAUNCH is not a test result. The launch
// error must not write lastBuildFailed / realBuildOrTestRunThisRun; only a
// completed run_command execution may.

import (
	"encoding/json"
	"testing"
)

func issue3840Reset(t *testing.T, a *Agent, tool, cmd string, failed bool) {
	t.Helper()
	a.maybeResetVerifyOnCommand(tool, json.RawMessage(`{"command":`+string(mustMarshal3840(cmd))+`}`), failed)
}

func mustMarshal3840(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

func TestIssue3840_BackgroundLaunchWritesNoEvidence(t *testing.T) {
	a := NewAgent(nil, nil, "sys", 5)
	// A failed foreground test sets the failure flag (baseline sanity).
	issue3840Reset(t, a, "run_command", "go test ./...", true)
	if !a.postEditVerify.lastBuildFailed {
		t.Fatal("baseline: run_command failure must set lastBuildFailed")
	}

	// Successful background LAUNCH of the same test must NOT clear the
	// failure flag (the background outcome is unknown). The real-evidence
	// gate write is deliberately KEPT for launches (#3796: spawn+read-back
	// is a legitimate verification pattern).
	a.postEditVerify.realBuildOrTestRunThisRun = false
	issue3840Reset(t, a, "start_command", "go test ./...", false)
	if !a.postEditVerify.lastBuildFailed {
		t.Fatal("launch success must not clear lastBuildFailed (background outcome unknown)")
	}
	if !a.postEditVerify.realBuildOrTestRunThisRun {
		t.Fatal("#3796: the launch still counts toward the evidence gate")
	}
}

func TestIssue3840_CounterStillResetsOnLaunch(t *testing.T) {
	a := NewAgent(nil, nil, "sys", 5)
	a.postEditVerify.sourceEditsSinceHint = 5
	issue3840Reset(t, a, "start_command", "go test ./...", false)
	if a.postEditVerify.sourceEditsSinceHint != 0 {
		t.Fatal("verify INTENT shown by the launch - counter must still reset")
	}
	if !a.postEditVerify.buildOrTestRunThisRun {
		t.Fatal("buildOrTestRunThisRun must still count the launch")
	}
}

func TestIssue3840_RunCommandUnchanged(t *testing.T) {
	a := NewAgent(nil, nil, "sys", 5)
	a.postEditVerify.lastBuildFailed = true
	issue3840Reset(t, a, "run_command", "go test ./...", false)
	if a.postEditVerify.lastBuildFailed {
		t.Fatal("a COMPLETED passing run_command must still clear the flag")
	}
	if !a.postEditVerify.realBuildOrTestRunThisRun {
		t.Fatal("a completed real test execution still satisfies the evidence gate")
	}
}
