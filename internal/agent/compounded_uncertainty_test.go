package agent

import (
	"strings"
	"testing"
)

func TestCompoundedUncertaintyState_Reset(t *testing.T) {
	s := newCompoundedUncertaintyState()
	s.totalWeight = 5.0
	s.fired = true

	s.reset()

	if s.totalWeight != 0 {
		t.Errorf("expected totalWeight=0 after reset, got %f", s.totalWeight)
	}
	if s.fired {
		t.Error("expected fired=false after reset")
	}
}

func TestRecordUncertainty(t *testing.T) {
	a := &Agent{
		compoundedUncert: newCompoundedUncertaintyState(),
	}

	// Record multiple uncertainty events.
	a.recordUncertainty("hedging", weightHedging)
	a.recordUncertainty("assumption", weightAssumption)
	a.recordUncertainty("false_premise", weightFalsePremise)

	expected := weightHedging + weightAssumption + weightFalsePremise
	if a.compoundedUncert.totalWeight != expected {
		t.Errorf("expected totalWeight=%f, got %f", expected, a.compoundedUncert.totalWeight)
	}
}

func TestRecordUncertainty_NilSafe(t *testing.T) {
	a := &Agent{}
	// Should not panic when compoundedUncert is nil.
	a.recordUncertainty("hedging", weightHedging)
}

func TestMaybeWarnCompoundedUncertainty_BelowThreshold(t *testing.T) {
	a := &Agent{
		compoundedUncert: newCompoundedUncertaintyState(),
	}

	// Accumulate just below threshold.
	a.recordUncertainty("hedging", weightHedging)
	a.recordUncertainty("assumption", weightAssumption)
	a.recordUncertainty("hedging", weightHedging)
	a.recordUncertainty("assumption", weightAssumption)
	// total = 4.0, below threshold of 5.5

	msg := a.maybeWarnCompoundedUncertainty()
	if msg != "" {
		t.Error("expected no warning below threshold")
	}
	if a.compoundedUncert.fired {
		t.Error("should not have fired below threshold")
	}
}

func TestMaybeWarnCompoundedUncertainty_AtThreshold(t *testing.T) {
	a := &Agent{
		compoundedUncert: newCompoundedUncertaintyState(),
	}

	// Accumulate above threshold.
	// 2 hedging (2.0) + 2 assumption (2.0) + 1 false_premise (2.0) = 6.0
	a.recordUncertainty("hedging", weightHedging)
	a.recordUncertainty("hedging", weightHedging)
	a.recordUncertainty("assumption", weightAssumption)
	a.recordUncertainty("assumption", weightAssumption)
	a.recordUncertainty("false_premise", weightFalsePremise)

	msg := a.maybeWarnCompoundedUncertainty()
	if msg == "" {
		t.Fatal("expected warning when above threshold")
	}
	if !a.compoundedUncert.fired {
		t.Error("expected fired=true after warning")
	}

	// Check message contains expected content.
	if !strings.Contains(msg, "compounded-trajectory-uncertainty") {
		t.Error("expected message to contain detector name")
	}
	if !strings.Contains(msg, "Spiral of Hallucination") {
		t.Error("expected message to reference Spiral of Hallucination")
	}
	if !strings.Contains(msg, "multiplicatively") {
		t.Error("expected message to mention multiplicative compounding")
	}
}

func TestMaybeWarnCompoundedUncertainty_FiresOnceOnly(t *testing.T) {
	a := &Agent{
		compoundedUncert: newCompoundedUncertaintyState(),
	}

	// Trigger once.
	for i := 0; i < 6; i++ {
		a.recordUncertainty("hedging", weightHedging)
	}

	msg1 := a.maybeWarnCompoundedUncertainty()
	if msg1 == "" {
		t.Fatal("expected first warning")
	}

	// Add more uncertainty - should NOT fire again.
	a.recordUncertainty("hedging", weightHedging)
	a.recordUncertainty("hedging", weightHedging)

	msg2 := a.maybeWarnCompoundedUncertainty()
	if msg2 != "" {
		t.Error("expected no second warning (fires once per run)")
	}
}

func TestMaybeWarnCompoundedUncertainty_NilSafe(t *testing.T) {
	a := &Agent{}
	msg := a.maybeWarnCompoundedUncertainty()
	if msg != "" {
		t.Error("expected empty string when compoundedUncert is nil")
	}
}

func TestMaybeWarnCompoundedUncertainty_ReliabilityMessage(t *testing.T) {
	a := &Agent{
		compoundedUncert: newCompoundedUncertaintyState(),
	}

	// With weight=6.0, compounded reliability = 0.85^6 = 0.377 = ~37%
	for i := 0; i < 6; i++ {
		a.recordUncertainty("hedging", weightHedging)
	}

	msg := a.maybeWarnCompoundedUncertainty()
	if msg == "" {
		t.Fatal("expected warning")
	}

	// Should mention a percentage (compounded reliability).
	if !strings.Contains(msg, "%") {
		t.Error("expected message to contain a reliability percentage")
	}
}

func TestMaybeWarnCompoundedUncertainty_MultipleCategories(t *testing.T) {
	a := &Agent{
		compoundedUncert: newCompoundedUncertaintyState(),
	}

	// Mix categories: hedging + assumption + false_premise + unverified_success
	a.recordUncertainty("hedging", weightHedging)
	a.recordUncertainty("assumption", weightAssumption)
	a.recordUncertainty("false_premise", weightFalsePremise)
	a.recordUncertainty("unverified_success", weightUnverifiedSucc)
	// total = 1.0 + 1.0 + 2.0 + 1.5 = 5.5

	msg := a.maybeWarnCompoundedUncertainty()
	if msg == "" {
		t.Error("expected warning with mixed categories at threshold")
	}
}
