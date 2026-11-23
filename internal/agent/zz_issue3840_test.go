package agent

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
	issue3840Reset(t, a, "start_command", "go test ./...", false)
	if !a.postEditVerify.realBuildOrTestRunThisRun {
		t.Fatal("#3796: the launch still counts toward the evidence gate")
	}
}

func TestIssue3840_LaunchCountsAsVerification(t *testing.T) {
	a := NewAgent(nil, nil, "sys", 5)
	issue3840Reset(t, a, "start_command", "go test ./...", false)
	if !a.postEditVerify.buildOrTestRunThisRun {
		t.Fatal("buildOrTestRunThisRun must still count the launch")
	}
}

func TestIssue3840_RunCommandUnchanged(t *testing.T) {
	a := NewAgent(nil, nil, "sys", 5)
	issue3840Reset(t, a, "run_command", "go test ./...", false)
	if !a.postEditVerify.realBuildOrTestRunThisRun {
		t.Fatal("a completed real test execution still satisfies the evidence gate")
	}
}
