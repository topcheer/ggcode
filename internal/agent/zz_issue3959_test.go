package agent

import (
	"os"
	"strings"
	"testing"
)

// TestIssue3959ResetCostWiring guards the per-run reset block: all three
// session-budget twins (token/time/cost) must be reset together. The #3959
// bug was resetSessionCostUsage() existing, tested, and documented as
// per-run, yet never called from production code - so the 80/95/100 tier
// flags stayed latched and the cost guardrail went permanently silent after
// the first over-limit run (#1494 case A replayed on the cost twin).
func TestIssue3959ResetCostWiring(t *testing.T) {
	b, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	src := string(b)
	block := src[strings.Index(src, "a.resetSessionTokenUsage()"):]
	window := block[:strings.Index(block, "unreadEdit.reset()")]
	for _, call := range []string{
		"a.resetSessionTokenUsage()",
		"a.resetSessionTimeUsage()",
		"a.resetSessionCostUsage()",
	} {
		if !strings.Contains(window, call) {
			t.Errorf("per-run reset block is missing %s (issue #3959 regression)", call)
		}
	}
}

// TestIssue3959CostStopArmsLoopGate guards the second wiring gap: the cost
// 100% stop branch must arm sessionBudgetStop exactly like the token and
// time twins (#3945 loop-top hard gate).
func TestIssue3959CostStopArmsLoopGate(t *testing.T) {
	b, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	src := string(b)
	i := strings.Index(src, "RecordSessionCostUsage(resp.Usage.InputTokens")
	if i < 0 {
		t.Fatalf("RecordSessionCostUsage call site not found")
	}
	// The stop branch spans from the call site to the following time-budget
	// twin; sessionBudgetStop must be set inside it.
	end := strings.Index(src[i:], "RecordSessionTimeUsage()")
	if end < 0 {
		t.Fatalf("time-budget twin call site not found")
	}
	branch := src[i : i+end]
	if !strings.Contains(branch, "sessionBudgetStop = true") {
		t.Errorf("cost 100%% stop branch does not arm sessionBudgetStop (issue #3959 regression)")
	}
}

// TestIssue3959CostLadderRearmsAfterReset pins the behavioral contract the
// wiring relies on: after resetSessionCostUsage() the ladder re-fires - the
// previously-latched warn80 tier must produce a warning again.
func TestIssue3959CostLadderRearmsAfterReset(t *testing.T) {
	a := &Agent{}
	a.SetSessionCostLimit(1.0, 1.0, 1.0, 0, 0) // $1.00 limit, $1.00 per M in/out
	// Jump straight past 100%: the ladder fires tiers in order, once each -
	// the 95% branch wins the first call (checks come before the stop check).
	msg, stop := a.RecordSessionCostUsage(2_000_000, 0, 0, 0) // 2M in = $2 = 200%
	if msg == "" || stop {
		t.Fatalf("expected 95%% tier on first over-limit call, got %q stop=%v", msg, stop)
	}
	// Next call crosses the stop tier (stopGiven still false).
	msg, stop = a.RecordSessionCostUsage(1_000, 0, 0, 0)
	if msg == "" || !stop {
		t.Fatalf("expected stop tier on second call, got %q stop=%v", msg, stop)
	}
	// Fully latched now: silence.
	if msg, _ := a.RecordSessionCostUsage(1_000, 0, 0, 0); msg != "" {
		t.Fatalf("expected silence while fully latched, got %q", msg)
	}
	// New run: reset re-arms the ladder - 90% must warn again.
	a.resetSessionCostUsage()
	msg, stop = a.RecordSessionCostUsage(900_000, 0, 0, 0) // 90% of $1
	if msg == "" || stop {
		t.Fatalf("expected a fresh 80%%-tier warning after reset - ladder did not re-arm, got %q stop=%v", msg, stop)
	}
}
