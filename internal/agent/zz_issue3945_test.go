package agent

import (
	"strings"
	"testing"
)

func TestIssue3945_StopGateWired(t *testing.T) {
	// #5543: the four arming sites moved into recordUsageBudgets
	// (run_usage_budget.go) and the loop gate stayed in agent.go; scan
	// both (shape-agnostic file targeting).
	raw, err := readStreamLoopSource("agent.go", "run_usage_budget.go")
	if err != nil {
		t.Fatal(err)
	}
	src := raw
	for _, needle := range []string{
		"sessionBudgetStop := false",
		"sessionBudgetStop = true",
		"if sessionBudgetStop {",
	} {
		if !strings.Contains(src, needle) {
			t.Fatalf("wiring missing %q", needle)
		}
	}

	// 4 explicit arming sites live in recordUsageBudgets; the main loop
	// propagates the folded result (sessionBudgetStop = true) once.
	if got := strings.Count(src, "sessionBudgetStop = true"); got != 5 {
		t.Fatalf("want 5 sessionBudgetStop = true sites (4 budget arming in recordUsageBudgets + 1 loop-fold in the main loop), got %d", got)
	}

	loopIdx := strings.Index(src, "for i := 0; a.maxIter <= 0 || i < a.maxIter; i++ {")
	if loopIdx < 0 {
		t.Fatal("run loop not found")
	}
	// The gate must live inside the run loop (after its opening). The
	// first gate hit beyond loopIdx is the main-loop budget-stop gate.
	gateIdx := strings.Index(src[loopIdx:], "if sessionBudgetStop {")
	if gateIdx < 0 {
		t.Fatal("budget-stop gate not found inside the run loop")
	}
}
