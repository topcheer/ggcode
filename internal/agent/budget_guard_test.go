package agent

import (
	"testing"
)

func TestBudgetGuard_NoWarningWithFewSteps(t *testing.T) {
	b := newBudgetGuardState()
	for i := 0; i < budgetMinSteps-1; i++ {
		b.recordStep(100)
	}
	// Should not fire with fewer than budgetMinSteps steps
	if w := b.maybeWarn(128000, 50000); w != "" {
		t.Errorf("expected no warning with %d steps, got warning", budgetMinSteps-1)
	}
}

func TestBudgetGuard_NoWarningWithStableCosts(t *testing.T) {
	b := newBudgetGuardState()
	// Record many steps with stable costs (~100 tokens each)
	for i := 0; i < 15; i++ {
		b.recordStep(100)
	}
	// Should not fire when costs are stable
	if w := b.maybeWarn(128000, 80000); w != "" {
		t.Errorf("expected no warning with stable costs, got: %s", w)
	}
}

func TestBudgetGuard_WarnsOnCostEscalation(t *testing.T) {
	b := newBudgetGuardState()
	// First several steps: low cost
	for i := 0; i < 6; i++ {
		b.recordStep(100)
	}
	// Recent steps: significantly higher cost (backtracking pattern)
	b.recordStep(300)
	b.recordStep(350)
	b.recordStep(400)

	// Context at 60% utilization (above threshold)
	w := b.maybeWarn(128000, 76800)
	if w == "" {
		t.Error("expected warning with escalating costs and high context utilization")
	}
	if !b.warningGiven {
		t.Error("warningGiven should be true after firing")
	}
}

func TestBudgetGuard_NoWarnAtLowContextUtilization(t *testing.T) {
	b := newBudgetGuardState()
	// Escalating costs but context only at 30%
	for i := 0; i < 6; i++ {
		b.recordStep(100)
	}
	b.recordStep(300)
	b.recordStep(350)
	b.recordStep(400)

	// Context at 30% utilization (below threshold)
	if w := b.maybeWarn(128000, 38400); w != "" {
		t.Error("expected no warning at low context utilization even with escalating costs")
	}
}

func TestBudgetGuard_FiresOncePerRun(t *testing.T) {
	b := newBudgetGuardState()
	for i := 0; i < 6; i++ {
		b.recordStep(100)
	}
	b.recordStep(300)
	b.recordStep(350)
	b.recordStep(400)

	// First call should fire
	w1 := b.maybeWarn(128000, 76800)
	if w1 == "" {
		t.Fatal("expected first warning")
	}

	// Second call should not fire (already given)
	w2 := b.maybeWarn(128000, 76800)
	if w2 != "" {
		t.Error("expected no second warning after already fired")
	}
}

func TestBudgetGuard_ResetClearsState(t *testing.T) {
	b := newBudgetGuardState()
	for i := 0; i < 10; i++ {
		b.recordStep(200)
	}
	b.warningGiven = true

	b.reset()

	if len(b.stepCosts) != 0 {
		t.Errorf("stepCosts not cleared: %d", len(b.stepCosts))
	}
	if b.totalConsumed != 0 {
		t.Errorf("totalConsumed not cleared: %d", b.totalConsumed)
	}
	if b.warningGiven {
		t.Error("warningGiven not cleared")
	}
}

func TestBudgetGuard_ComputeStatsStable(t *testing.T) {
	b := newBudgetGuardState()
	costs := []int{100, 110, 95, 105, 100, 110}
	for _, c := range costs {
		b.recordStep(c)
	}

	overall, recent, escalating := b.computeStats()
	if escalating {
		t.Errorf("expected no escalation with stable costs: overall=%.1f recent=%.1f", overall, recent)
	}
	// Overall avg should be around 103.3
	if overall < 90 || overall > 120 {
		t.Errorf("overall avg unexpected: %.1f", overall)
	}
}

func TestBudgetGuard_ComputeStatsEscalating(t *testing.T) {
	b := newBudgetGuardState()
	costs := []int{100, 90, 110, 95, 100, 105, 400, 500, 600}
	for _, c := range costs {
		b.recordStep(c)
	}

	overall, recent, escalating := b.computeStats()
	if !escalating {
		t.Errorf("expected escalation with rising costs: overall=%.1f recent=%.1f", overall, recent)
	}
	// Recent avg should be around 500
	if recent < 400 {
		t.Errorf("recent avg unexpected: %.1f", recent)
	}
	// Overall avg should be around 222
	if overall < 150 || overall > 300 {
		t.Errorf("overall avg unexpected: %.1f", overall)
	}
}

func TestBudgetGuard_ZeroContextWindow(t *testing.T) {
	b := newBudgetGuardState()
	// Escalating costs, but context window is 0 (unknown)
	for i := 0; i < 6; i++ {
		b.recordStep(100)
	}
	b.recordStep(300)
	b.recordStep(350)
	b.recordStep(400)

	// With contextWindow=0, the utilization check is skipped,
	// so the warning should still fire
	w := b.maybeWarn(0, 0)
	if w == "" {
		t.Error("expected warning with unknown context window (utilization check skipped)")
	}
}

func TestBudgetGuard_WarningContainsDiagnostics(t *testing.T) {
	b := newBudgetGuardState()
	for i := 0; i < 6; i++ {
		b.recordStep(100)
	}
	b.recordStep(300)
	b.recordStep(350)
	b.recordStep(400)

	w := b.maybeWarn(128000, 76800)
	if w == "" {
		t.Fatal("expected warning")
	}
	// Warning should contain "budget guard" prefix
	if !containsStr(w, "budget guard") {
		t.Error("warning should contain 'budget guard' identifier")
	}
	// Warning should mention cost escalation
	if !containsStr(w, "escalat") {
		t.Error("warning should mention escalation")
	}
}

// containsStr and indexStr are already defined in reflection_test.go
