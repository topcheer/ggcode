package agent

import (
	"strings"
	"testing"
)

func TestIssue4787_ClassifierSpawnEnoentIsSystemicNotStructural(t *testing.T) {

	got := classifyFailureMode("run_command", "fork/exec /bin/sh: no such file or directory")
	if got != FailureModeSystemic {
		t.Fatalf("S-16 spawn failure must classify SYSTEMIC (environment), got %v", got)
	}
}

func TestIssue4787_ClassifierWrongPathStillStructural(t *testing.T) {

	got := classifyFailureMode("run_command", "cat /no/such/file: no such file or directory")
	if got != FailureModeStructural {
		t.Fatalf("argument-path ENOENT must stay STRUCTURAL, got %v", got)
	}
	got = classifyFailureMode("read_file", "open /no/such/file: no such file or directory")
	if got != FailureModeStructural {
		t.Fatalf("read_file ENOENT must stay STRUCTURAL, got %v", got)
	}

	got = classifyFailureMode("run_command", "fork/exec /missing/binary: no such file or directory")
	if got != FailureModeStructural {
		t.Fatalf("direct wrong-binary spawn must stay STRUCTURAL, got %v", got)
	}
	got = classifyFailureMode("run_command", "fork/exec ./build/tool: no such file or directory")
	if got != FailureModeStructural {
		t.Fatalf("relative wrong-binary spawn must stay STRUCTURAL, got %v", got)
	}
}

func TestIssue4787_ClassifierShellVariantsSystemic(t *testing.T) {

	for _, sig := range []string{
		"fork/exec /bin/sh: no such file or directory",
		"fork/exec /bin/bash: no such file or directory",
		"fork/exec /usr/bin/zsh: no such file or directory",
		"fork/exec /bin/dash: no such file or directory",
		"fork/exec pwsh: no such file or directory",
		"fork/exec C:/Program Files/PowerShell/7/pwsh.exe: no such file or directory",
		"fork/exec /powershell.exe: no such file or directory",
	} {
		if got := classifyFailureMode("run_command", sig); got != FailureModeSystemic {
			t.Fatalf("shell spawn failure %q must classify SYSTEMIC, got %v", sig, got)
		}
	}
}

func TestIssue4787_StagnationExemptsShellHealthProbe(t *testing.T) {
	s := newStrategyStagnationState()
	probe := `{"command":"echo ggcode-shell-health-probe-ok"}`

	for i := 0; i < 5; i++ {
		if s.recordAttemptDetail("run_command", probe, false, "fork/exec /bin/sh: no such file or directory") {
			t.Fatalf("probe failure %d must not trigger stagnation", i+1)
		}
	}
}

func TestIssue4787_StagnationExemptsShellOutageFailures(t *testing.T) {
	s := newStrategyStagnationState()
	args := `{"command":"go test ./..."}`
	errContent := "fork/exec /bin/sh: no such file or directory"

	for i := 0; i < 5; i++ {
		if s.recordAttemptDetail("run_command", args, false, errContent) {
			t.Fatalf("outage failure %d must not trigger stagnation", i+1)
		}
	}
}

func TestIssue4787_StagnationStillFiresForRealRuts(t *testing.T) {
	s := newStrategyStagnationState()
	args := `{"command":"go build ./..."}`

	s.recordAttemptDetail("run_command", args, false, "exit status 1")
	s.recordAttemptDetail("run_command", args, false, "exit status 1")
	if !s.recordAttemptDetail("run_command", args, false, "exit status 1") {
		t.Fatal("third consecutive genuine failure must still fire stagnation")
	}
}

func TestIssue4787_SystemicGuidanceForSpawnFailure(t *testing.T) {

	fm := newFailureModeState()
	g := fm.recordResult("run_command", true, "fork/exec /bin/sh: no such file or directory")
	if !strings.Contains(g, "SYSTEMIC") {
		t.Fatalf("expected SYSTEMIC guidance for S-16 spawn failure, got: %q", g)
	}
}

func TestIssue4787_ShellOutageFailureHelper(t *testing.T) {
	if !isShellOutageFailure("fork/exec /bin/sh: no such file or directory") {
		t.Fatal("verbatim S-16 signature must match")
	}
	if isShellOutageFailure("chdir /gone: no such file or directory") {
		t.Fatal("chdir ENOENT is a dead-cwd problem (NearestLiveDir), not an outage")
	}
	if isShellOutageFailure("") {
		t.Fatal("empty content must not match")
	}
}

func TestIssue4787_StagnationExemptionsAreFailureOnly(t *testing.T) {

	s := newStrategyStagnationState()
	args := `{"command":"go test ./..."}`
	errContent := "fork/exec /bin/sh: no such file or directory"
	s.recordAttemptDetail("run_command", args, false, errContent)
	s.recordAttemptDetail("run_command", args, false, errContent)

	s.recordAttemptDetail("run_command", args, true, "ok\n"+errContent)

	if s.recordAttemptDetail("run_command", args, false, errContent) {
		t.Fatal("failure after a recorded success must not fire stagnation (chain was broken)")
	}
}
