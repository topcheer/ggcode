package agent

import (
	"strings"
	"testing"
)

// Delegation budgets: sub-agent token spend recorded via
// RecordDelegatedTokenUsage must count toward the session token budget
// 80/95/100% ladder (not bypass it), be separately attributable via
// DelegatedTokenUsage, be drainable as a pending alert by the agent loop,
// and be cleared by resetSessionTokenUsage.
func TestDelegatedTokenUsageCountsTowardBudgetLadder(t *testing.T) {
	a := &Agent{}
	defer a.Close()

	setAgentSessionTokenBudget(a, 1000)

	// Main-loop spend stays unattributed to delegation.
	if msg, stop := a.RecordSessionTokenUsage(500, 0); msg != "" || stop {
		t.Fatalf("500/1000 should not warn yet, got msg=%q stop=%v", msg, stop)
	}
	if got := a.DelegatedTokenUsage(); got != 0 {
		t.Fatalf("delegated usage before delegation = %d, want 0", got)
	}

	// Sub-agent spend lands in the budget and crosses 80%.
	a.RecordDelegatedTokenUsage(200, 200) // 900/1000 = 90%
	if got := a.DelegatedTokenUsage(); got != 400 {
		t.Fatalf("delegated usage = %d, want 400", got)
	}
	msg, stop := a.SessionTokenBudgetAlert()
	if msg == "" || stop {
		t.Fatalf("delegated crossing 90%% should park a warn alert, got msg=%q stop=%v", msg, stop)
	}
	if !strings.Contains(msg, "80%") && !strings.Contains(msg, "95%") {
		t.Fatalf("alert should mention a budget threshold, got %q", msg)
	}
	// Alert drains: second read is empty.
	if msg2, _ := a.SessionTokenBudgetAlert(); msg2 != "" {
		t.Fatalf("pending alert should drain after one read, got %q", msg2)
	}

	// Further delegated spend crosses 95% first, then hard-stops at 100% on
	// the next crossing (#543 contract: 95% finalize fires once, then stop).
	a.RecordDelegatedTokenUsage(200, 0) // 1100/1000 = 110%
	msg, stop = a.SessionTokenBudgetAlert()
	if !strings.Contains(msg, "95%") || stop {
		t.Fatalf("first crossing past 95%% should park the finalize alert, got msg=%q stop=%v", msg, stop)
	}
	a.RecordDelegatedTokenUsage(1, 0)
	msg, stop = a.SessionTokenBudgetAlert()
	if !stop {
		t.Fatalf("second crossing should park stop=true, got msg=%q stop=%v", msg, stop)
	}

	// Reset re-arms the ladder and clears attribution.
	a.resetSessionTokenUsage()
	if got := a.DelegatedTokenUsage(); got != 0 {
		t.Fatalf("delegated usage after reset = %d, want 0", got)
	}
	if msg, stop := a.SessionTokenBudgetAlert(); msg != "" || stop {
		t.Fatalf("reset should clear pending alerts, got msg=%q stop=%v", msg, stop)
	}
}

// Without a budget configured, delegated recording must be inert (no alerts).
func TestDelegatedTokenUsageInertWithoutBudget(t *testing.T) {
	a := &Agent{}
	defer a.Close()

	a.RecordDelegatedTokenUsage(10_000, 5_000)
	if got := a.DelegatedTokenUsage(); got != 15_000 {
		t.Fatalf("attribution should still accumulate without budget, got %d", got)
	}
	if msg, stop := a.SessionTokenBudgetAlert(); msg != "" || stop {
		t.Fatalf("no budget = no alert, got msg=%q stop=%v", msg, stop)
	}
}
