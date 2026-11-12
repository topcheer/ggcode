package agent

import (
	"strings"
	"testing"
)

func TestSessionCostLimitResetWiredInRunStart(t *testing.T) {
	src, err := readAgentAndProcessingSources()
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	text := string(src)

	for _, call := range []string{
		"a.resetSessionTokenUsage()",
		"a.resetSessionTimeUsage()",
		"a.resetSessionCostUsage()",
	} {
		if !strings.Contains(text, call) {
			t.Errorf("per-run reset block is missing %s - tier flags latch across runs (#1494 case A)", call)
		}
	}

	anchor := strings.Index(text, "a.resetSessionTokenUsage()")
	if anchor < 0 {
		t.Fatal("token reset anchor not found")
	}
	window := text[anchor:min(anchor+800, len(text))]
	if !strings.Contains(window, "a.resetSessionCostUsage()") {
		t.Error("a.resetSessionCostUsage() is not adjacent to the token/time reset twins - verify it sits in the same per-run reset block")
	}
}

func TestSessionCostLimitStopArmsLoopGate(t *testing.T) {
	src, err := readAgentAndProcessingSources()
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	text := string(src)

	anchor := strings.Index(text, "a.RecordSessionCostUsage(")
	if anchor < 0 {
		t.Fatal("RecordSessionCostUsage call site not found in run loop")
	}

	end := strings.Index(text[anchor:], "a.RecordSessionTimeUsage(")
	if end < 0 {
		t.Fatal("time budget twin not found after cost record site")
	}
	branch := text[anchor : anchor+end]
	if !strings.Contains(branch, "sessionBudgetStop = true") {
		t.Error("cost 100%% stop branch does not arm sessionBudgetStop - weaker stop semantics than token/time twins (#3945 parity)")
	}
}

func TestIssue3959CostLadderRearmsAfterReset(t *testing.T) {
	a := &Agent{}
	defer agentSessionCostLimits.Delete(a)

	a.SetSessionCostLimit(1.00, 10, 0, 0, 0)
	msg, stop := a.RecordSessionCostUsage(2_000_000, 0, 0, 0)
	if msg == "" || stop || !strings.Contains(msg, "95%") {
		t.Fatalf("200%% first call: got msg=%q stop=%v, want 95%% guidance (tier precedence)", msg, stop)
	}
	msg, stop = a.RecordSessionCostUsage(1_000, 0, 0, 0)
	if msg == "" || !stop || !strings.Contains(msg, "exhausted") {
		t.Fatalf("second call: got msg=%q stop=%v, want exhausted stop", msg, stop)
	}

	if msg, stop := a.RecordSessionCostUsage(1_000, 0, 0, 0); msg != "" || stop {
		t.Fatalf("post-stop: got msg=%q stop=%v, want silence (latched)", msg, stop)
	}

	a.resetSessionCostUsage()

	msg, stop = a.RecordSessionCostUsage(85_000, 0, 0, 0)
	if msg == "" || stop || !strings.Contains(msg, "80%") {
		t.Fatalf("post-reset 85%%: got msg=%q stop=%v, want 80%% guidance re-fired", msg, stop)
	}
}
