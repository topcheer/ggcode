package agent

import (
	"strings"
	"testing"
)

// #4787 (S-16): during shell-outage windows (wholesale fork/exec-ENOENT,
// 1-8min, self-healing), honest retry behavior was punished by two
// detectors: the failure-mode classifier branded spawn failures STRUCTURAL
// ("do NOT retry the same approach") and the strategy-stagnation gate cut
// health-probe retries as a "local minimum". Both must exempt the S-16
// signature.

func TestIssue4787_ClassifierSpawnEnoentIsSystemicNotStructural(t *testing.T) {
	// The verbatim S-16 error: "fork/exec /bin/sh: no such file or
	// directory" (note: no "exec:" token — that's why #335's whole-phrase
	// guard never caught it and it fell through to STRUCTURAL).
	got := classifyFailureMode("run_command", "fork/exec /bin/sh: no such file or directory")
	if got != FailureModeSystemic {
		t.Fatalf("S-16 spawn failure must classify SYSTEMIC (environment), got %v", got)
	}
}

func TestIssue4787_ClassifierWrongPathStillStructural(t *testing.T) {
	// Guard the #335 contract: plain path-ENOENT in run_command output
	// stays STRUCTURAL (agent-fixable), not systemic.
	got := classifyFailureMode("run_command", "cat /no/such/file: no such file or directory")
	if got != FailureModeStructural {
		t.Fatalf("argument-path ENOENT must stay STRUCTURAL, got %v", got)
	}
	got = classifyFailureMode("read_file", "open /no/such/file: no such file or directory")
	if got != FailureModeStructural {
		t.Fatalf("read_file ENOENT must stay STRUCTURAL, got %v", got)
	}
}

func TestIssue4787_StagnationExemptsShellHealthProbe(t *testing.T) {
	s := newStrategyStagnationState()
	probe := `{"command":"echo ggcode-shell-health-probe-ok"}`
	// 5 consecutive probe failures (way past the threshold of 3) must NOT
	// fire the stagnation gate: probing IS the recovery strategy.
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
	// Legitimate commands failing during an outage window must not be
	// branded a local minimum either.
	for i := 0; i < 5; i++ {
		if s.recordAttemptDetail("run_command", args, false, errContent) {
			t.Fatalf("outage failure %d must not trigger stagnation", i+1)
		}
	}
}

func TestIssue4787_StagnationStillFiresForRealRuts(t *testing.T) {
	s := newStrategyStagnationState()
	args := `{"command":"go build ./..."}`
	// Same-command failures with a NORMAL error (exit status 1) still chain
	// into stagnation — the exemption must not swallow genuine ruts.
	s.recordAttemptDetail("run_command", args, false, "exit status 1")
	s.recordAttemptDetail("run_command", args, false, "exit status 1")
	if !s.recordAttemptDetail("run_command", args, false, "exit status 1") {
		t.Fatal("third consecutive genuine failure must still fire stagnation")
	}
}

func TestIssue4787_SystemicGuidanceForSpawnFailure(t *testing.T) {
	// recordResult with the S-16 signature must produce environment-level
	// guidance (verify environment / report if persistent), not the
	// structural "do NOT retry" verdict.
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
	// The exemptions must not swallow SUCCESSFUL attempts: a recorded
	// success is what breaks a failure chain. If a success quoting the
	// S-16 signature were skipped (e.g. running this repo's own tests),
	// stale failure chains would survive and later fire spuriously.
	s := newStrategyStagnationState()
	args := `{"command":"go test ./..."}`
	errContent := "fork/exec /bin/sh: no such file or directory"
	s.recordAttemptDetail("run_command", args, false, errContent)
	s.recordAttemptDetail("run_command", args, false, errContent)
	// Success whose output quotes the signature must be RECORDED...
	s.recordAttemptDetail("run_command", args, true, "ok\n"+errContent)
	// ...so this fresh failure is chain entry #1, not #3: no gate.
	if s.recordAttemptDetail("run_command", args, false, errContent) {
		t.Fatal("failure after a recorded success must not fire stagnation (chain was broken)")
	}
}
