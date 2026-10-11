package agent

// #3959 companion tests: the cost ladder (#3922) must be wired exactly like
// its token/time twins - per-run reset in the run-start block AND the 100%
// stop branch arming the sessionBudgetStop loop-top gate (#3945).
// The bugs were wiring gaps (written+tested but never called in production),
// so the regression guards are source-level co-location checks plus a
// behavioral latch-recovery probe.

import (
	"os"
	"strings"
	"testing"
)

// Behavioral: after the 100% hard-stop latches the tier flags, the per-run
// reset must fully re-arm the ladder - a later run can fire stop again.
func TestIssue3959_StopLatchRecoveredByRunReset(t *testing.T) {
	a := &Agent{}
	defer agentSessionCostLimits.Delete(a)
	a.SetSessionCostLimit(1.00, 10, 0, 0, 0)

	// Run 1: walk the ladder stepwise (each call fires the first un-latched
	// tier) to 100% -> hard stop, stopGiven latches.
	for _, tc := range []struct {
		tokens int
		want   string
	}{{85_000, "80%"}, {10_000, "95%"}, {25_000, "exhausted"}} {
		msg, stop := a.RecordSessionCostUsage(tc.tokens, 0, 0, 0)
		if msg == "" || !strings.Contains(msg, tc.want) {
			t.Fatalf("run1 %s: got msg=%q, want %q", tc.want, msg, tc.want)
		}
		if tc.want == "exhausted" && !stop {
			t.Fatalf("run1 100%%: stop=false, want hard stop")
		}
		if tc.want != "exhausted" && stop {
			t.Fatalf("run1 %s: stop=true, want guidance only", tc.want)
		}
	}
	// Latched: any further record is silent (the bug's silent window).
	if msg, stop := a.RecordSessionCostUsage(120_000, 0, 0, 0); msg != "" || stop {
		t.Fatalf("latched: got msg=%q stop=%v, want silence", msg, stop)
	}
	// Per-run reset (the wiring this fix restores at run start).
	a.resetSessionCostUsage()
	// Run 2: ladder fully re-armed - guidance and stop both fire again.
	if msg, stop := a.RecordSessionCostUsage(85_000, 0, 0, 0); !strings.Contains(msg, "80%") || stop {
		t.Fatalf("run2 80%%: got msg=%q stop=%v, want 80%% guidance", msg, stop)
	}
	if msg, stop := a.RecordSessionCostUsage(15_000, 0, 0, 0); !strings.Contains(msg, "95%") || stop {
		t.Fatalf("run2 95%%: got msg=%q stop=%v, want 95%% guidance", msg, stop)
	}
	if msg, stop := a.RecordSessionCostUsage(30_000, 0, 0, 0); msg == "" || !stop || !strings.Contains(msg, "exhausted") {
		t.Fatalf("run2 100%%: got msg=%q stop=%v, want exhausted stop again", msg, stop)
	}
}

// Source guard: the per-run reset block must contain all THREE ladder twins -
// token, time, and cost. The cost twin was missing until #3959.
func TestIssue3959_RunResetBlockHasAllThreeTwins(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Skipf("agent.go not readable: %v", err)
	}
	text := string(src)
	for _, fn := range []string{"resetSessionTokenUsage()", "resetSessionTimeUsage()", "resetSessionCostUsage()"} {
		if !strings.Contains(text, "a."+fn) {
			t.Fatalf("per-run reset wiring lost: a.%s not found in agent.go", fn)
		}
	}
	// Co-location: all three within the same reset block (600-char window
	// after the first token reset call; the twins sit a few lines apart with
	// their rationale comments).
	tokIdx := strings.Index(text, "a.resetSessionTokenUsage()")
	window := text[tokIdx:min(tokIdx+600, len(text))]
	if !strings.Contains(window, "a.resetSessionTimeUsage()") || !strings.Contains(window, "a.resetSessionCostUsage()") {
		t.Fatal("per-run reset block incomplete: time/cost twins not co-located with token twin within 200 lines")
	}
}

// Source guard: the 100% cost stop branch must arm sessionBudgetStop like the
// token/time twins (#3945 loop-top gate).
func TestIssue3959_CostStopBranchArmsGate(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Skipf("agent.go not readable: %v", err)
	}
	text := string(src)
	needle := "Session spend limit fully consumed"
	idx := strings.Index(text, needle)
	if idx < 0 {
		t.Fatal("cost stop branch message not found")
	}
	// The stop branch opens before the message; the gate assignment must sit
	// between the branch open and the message (within 200 chars before).
	start := max(0, idx-200)
	if !strings.Contains(text[start:idx], "sessionBudgetStop = true") {
		t.Fatal("cost stop branch does not arm sessionBudgetStop (loop-top gate unarmed for spend limit)")
	}
}
