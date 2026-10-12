package agent

import (
	"os"
	"strings"
	"testing"
)

// #4072: a sub-agent goroutine crossing the 100% budget hard-stop consumes
// the stopGiven once-flag inside checkLocked. Every later main-path
// RecordSessionTokenUsage then returns ("", false) forever, so the ONLY
// place the run can learn "stop" is the delegated drain itself - it must arm
// the loop-top gate (sessionBudgetStop = true), exactly like the token,
// cost, and time twins.
//
// Part 1 pins the root-cause chain at behavior level: once delegated spend
// has drained a stop=true alert, subsequent main-path accounting is silent.
// Part 2 pins the fix at source level: the delegated drain's stop branch
// contains the arming assignment (the whole bug was this one missing line).
func TestIssue4072_DelegatedStopConsumesOnceFlag_MainPathNeverReEmits(t *testing.T) {
	a := &Agent{}
	defer a.Close()

	setAgentSessionTokenBudget(a, 1000)

	// Sub-agent goroutine drives the ladder past 100%: 95% alert first
	// (checkLocked contract), then the hard stop on the next crossing.
	a.RecordDelegatedTokenUsage(1200, 0) // 1200/1000 = 120%
	msg, stop := a.SessionTokenBudgetAlert()
	if !strings.Contains(msg, "95%") || stop {
		t.Fatalf("first crossing should park the 95%% finalize alert, got msg=%q stop=%v", msg, stop)
	}
	a.RecordDelegatedTokenUsage(1, 0)
	msg, stop = a.SessionTokenBudgetAlert()
	if !stop {
		t.Fatalf("second crossing should park stop=true, got msg=%q stop=%v", msg, stop)
	}

	// Root cause chain: the main path can NEVER re-emit the stop. Once the
	// sub-agent side consumed stopGiven, RecordSessionTokenUsage is silent
	// no matter how much the main agent spends - so the drain site is the
	// only remaining place that can arm the loop-top gate.
	for i, spend := range []int64{50, 500, 5000} {
		if msg, stop := a.RecordSessionTokenUsage(spend, 0); msg != "" || stop {
			t.Fatalf("main-path accounting after delegated stop must be silent (iteration %d), got msg=%q stop=%v", i, msg, stop)
		}
	}
	// And the pending alert is already drained - nothing is left for a
	// later iteration to observe.
	if msg, stop := a.SessionTokenBudgetAlert(); msg != "" || stop {
		t.Fatalf("pending alert must be empty after drain, got msg=%q stop=%v", msg, stop)
	}
}

// Source-level guard: the delegated drain block's stop branch must arm the
// loop-top gate. Locates the drain call and asserts the arming assignment
// appears before the block hands control to the next ladder section.
func TestIssue4072_DelegatedDrainStopBranchArmsLoopTopGate(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	s := string(src)

	drainIdx := strings.Index(s, "if msg, stop := a.SessionTokenBudgetAlert(); msg != \"\" {")
	if drainIdx < 0 {
		t.Fatal("delegated drain block (SessionTokenBudgetAlert) not found in agent.go")
	}
	// The drain block ends where the next ladder section begins (r415 time
	// ladder comment follows it in the source).
	nextIdx := strings.Index(s[drainIdx:], "// r415: time-dimension soft ladder")
	if nextIdx < 0 {
		t.Fatal("r415 time-ladder section not found after the delegated drain block")
	}
	block := s[drainIdx : drainIdx+nextIdx]

	if !strings.Contains(block, "if stop {") {
		t.Fatal("delegated drain block must have a stop branch")
	}
	if !strings.Contains(block, "sessionBudgetStop = true") {
		t.Fatal("#4072 regression: delegated drain stop branch does not arm the loop-top gate (sessionBudgetStop = true missing) - without it a sub-agent-driven 100% crossing can never stop the run (stopGiven once-flag already consumed)")
	}
}
