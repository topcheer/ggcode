package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// --- #490: plan_abandon 100% false positive on faithful completion ---

func TestPlanAbandon_EvidenceGateMatrix(t *testing.T) {
	plan := "Here's my plan:\n" +
		"1. I'll read the auth module\n" +
		"2. Next, I'll fix the token refresh logic\n" +
		"3. Finally, I'll run the tests to verify"
	completion := "The task is now complete."

	cases := []struct {
		name string
		rs   *RunStats
		want bool // want warning?
	}{
		{"nil stats = no evidence channel = warn (true abandonment shape)", nil, true},
		{"full evidence = faithful completion = silent", &RunStats{
			FilesEdited: []string{"token.go"},
			CommandsRun: []string{"go test ./..."},
		}, false},
		{"edit evidence only, but plan also had run step = gap = warn", &RunStats{
			FilesEdited: []string{"token.go"},
		}, true},
		{"command evidence only, but plan also had edit step = gap = warn", &RunStats{
			CommandsRun: []string{"go test ./..."},
		}, true},
		{"zero evidence = warn", &RunStats{}, true},
	}
	for _, tc := range cases {
		a := &Agent{planAbandon: newPlanAbandonState()}
		if h := a.maybeWarnPlanAbandon(plan, tc.rs); h != "" {
			t.Errorf("%s: plan declaration must not warn", tc.name)
		}
		h := a.maybeWarnPlanAbandon(completion, tc.rs)
		if tc.want && h == "" {
			t.Errorf("%s: expected warning, got none", tc.name)
		}
		if !tc.want && h != "" {
			t.Errorf("%s: unexpected warning: %s", tc.name, h)
		}
	}
}

// Pure-read plans have no evidence channel — never warn on them.
func TestPlanAbandon_PureReadPlanSilent(t *testing.T) {
	a := &Agent{planAbandon: newPlanAbandonState()}
	// Careful wording: no edit verbs, no run/test/verify verbs.
	plan := "1. I'll read the auth module\n2. Next, I'll inspect the callers\n3. Finally, I'll examine the handler wiring"
	rs := &RunStats{} // nothing happened at all
	if h := a.maybeWarnPlanAbandon(plan, rs); h != "" {
		t.Fatalf("plan declaration must not warn: %s", h)
	}
	if h := a.maybeWarnPlanAbandon("The task is now complete.", rs); h != "" {
		t.Fatalf("pure-read plan has no evidence channel; completion must stay silent, got: %s", h)
	}
}

// --- #491: correction_spiral shell-channel filtering ---

// Sub-problem A: a successful cat between edit and build must NOT break
// the correction chain.
func TestCorrectionSpiral_CatDoesNotBreakChain(t *testing.T) {
	s := newCorrectionSpiralState()
	// Simulate the production wiring: only run_command + psIsVerifyCommand
	// feeds recordVerifyResult; the interleaved successful `cat` calls are
	// filtered out before it. Escalating severities: syntax → compile → runtime.
	s.recordEdit(1)
	s.recordVerifyResult("run_command", "syntax error: unexpected semicolon", true, 2)
	s.recordEdit(3)
	s.recordVerifyResult("run_command", "./x.go:12: undefined: foo", true, 4)
	s.recordEdit(5)
	s.recordVerifyResult("run_command", "panic: runtime error: index out of range", true, 6)
	if len(s.errorSequence) != 3 {
		t.Fatalf("real failures must accumulate to 3, got %d", len(s.errorSequence))
	}
	if msg := s.maybeWarn(7); msg == "" {
		t.Fatal("escalating real failures must trigger the spiral warning")
	}
}

// Sub-problem C: "--- FAIL: TestSignalHandler" must classify as test
// failure, not crash; bare "signal" mentions no longer crash.
func TestCorrectionSpiral_SignalSeverity(t *testing.T) {
	if got := csClassifySeverity("--- FAIL: TestSignalHandler\n    handler_test.go:12: bad state"); got != sevTest {
		t.Fatalf("test failure containing 'signal' in test NAME must be sevTest, got %d", got)
	}
	if got := csClassifySeverity("signal handling is documented in README.md"); got == sevCrash {
		t.Fatal("bare prose mention of 'signal' must not classify as crash")
	}
	// Real crash forms still classify.
	for _, c := range []string{"fatal error: signal: segmentation violation", "runtime: received signal SIGSEGV", "panic: signal: killed"} {
		if got := csClassifySeverity(c); got != sevCrash {
			t.Errorf("real crash form %q must stay sevCrash, got %d", c, got)
		}
	}
}

// The production gate shape: csIsVerifyTool is gone; the wiring filters
// via psIsVerifyCommand on the command content.
func TestCorrectionSpiral_WiringGateShape(t *testing.T) {
	for _, cmd := range []string{"cat config.yaml", "ls -la", "git log --oneline", "rg TODO internal/"} {
		if psIsVerifyCommand(cmd) {
			t.Errorf("%q must NOT pass psIsVerifyCommand (would pollute/break the spiral chain)", cmd)
		}
	}
	for _, cmd := range []string{"go test ./...", "go build ./..."} {
		if !psIsVerifyCommand(cmd) {
			t.Errorf("%q must pass psIsVerifyCommand", cmd)
		}
	}
}

// --- #492: momentum_loss shell exploration ---

// The issue's exact scenario: 30 productive iterations, then pure shell
// exploration (cat/ls/git log) — must trigger last-mile stall.
func TestMomentumLoss_ShellExplorationTriggersStall(t *testing.T) {
	m := newMomentumLossState()
	maxIter := 50
	// Iterations 1-30: 2 edits each.
	for it := 1; it <= 30; it++ {
		m.startIteration(it)
		m.recordToolCall("edit_file", nil)
		m.recordToolCall("edit_file", nil)
	}
	// Iterations 31-40: pure shell exploration.
	for it := 31; it <= 40; it++ {
		m.startIteration(it)
		m.recordToolCall("run_command", json.RawMessage(`{"command":"cat internal/agent/agent.go"}`))
		m.recordToolCall("run_command", json.RawMessage(`{"command":"git log --oneline -5"}`))
		m.recordToolCall("run_command", json.RawMessage(`{"command":"rg TODO internal/"}`))
	}
	msg := m.checkMomentumLoss(maxIter)
	if msg == "" {
		t.Fatal("late-phase shell exploration must trigger last-mile stall (#492): detector was blind on the shell channel")
	}
	if !strings.Contains(msg, "momentum") && !strings.Contains(msg, "stall") {
		t.Fatalf("unexpected message: %s", msg)
	}
}

func TestMomentumLoss_VerifyCommandsStayProductive(t *testing.T) {
	m := newMomentumLossState()
	maxIter := 50
	for it := 1; it <= 30; it++ {
		m.startIteration(it)
		m.recordToolCall("edit_file", nil)
	}
	for it := 31; it <= 40; it++ {
		m.startIteration(it)
		m.recordToolCall("run_command", json.RawMessage(`{"command":"go test ./..."}`))
	}
	if msg := m.checkMomentumLoss(maxIter); msg != "" {
		t.Fatalf("genuine verify commands are productive — no stall must fire, got: %s", msg)
	}
}

func TestMomentumLoss_ObservationalClassifier(t *testing.T) {
	for _, cmd := range []string{
		"cat x.go", "ls -la", "head -20 f", "tail -5 f", "pwd",
		"rg pattern .", "grep foo dir", "find . -name x", "ag TODO",
		"git log --oneline", "git diff HEAD", "git show abc", "git status", "git blame f.go",
	} {
		if !mlIsObservationalCommand(cmd) {
			t.Errorf("%q must be observational", cmd)
		}
	}
	for _, cmd := range []string{
		"go test ./...", "go build ./...", "make verify", "git add .", "git commit -m x",
		"npm test", "cargo build", "",
	} {
		if mlIsObservationalCommand(cmd) {
			t.Errorf("%q must NOT be observational (productive/unknown stays productive)", cmd)
		}
	}
}
