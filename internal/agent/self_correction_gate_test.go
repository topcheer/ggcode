package agent

import (
	"strings"
	"testing"
)

func TestSelfCorrectionGate_NoWarningBelowMinRounds(t *testing.T) {
	g := newSelfCorrectionGateState()

	// Round 1: 2 new, 0 resolved (net-negative) — but too few rounds.
	msg := g.recordRound(2, 1, 0)
	if msg != "" {
		t.Fatalf("expected no warning before min rounds, got: %s", msg)
	}

	// Round 2: still below threshold count.
	msg = g.recordRound(2, 1, 0)
	if msg != "" {
		t.Fatalf("expected no warning before min rounds, got: %s", msg)
	}
}

func TestSelfCorrectionGate_FiresWhenNetNegative(t *testing.T) {
	g := newSelfCorrectionGateState()

	// Simulate 3 correction rounds where each introduces 2 new errors
	// but only resolves 0-1. EIR=2.0, ECR=0.33, ratio=0.17 < 1.2.
	g.recordRound(2, 1, 0)        // round 1
	g.recordRound(2, 2, 1)        // round 2
	msg := g.recordRound(2, 2, 0) // round 3 — should fire

	if msg == "" {
		t.Fatal("expected stability warning for net-negative self-correction")
	}
	if !strings.Contains(msg, "SELF-CORRECTION UNSTABLE") {
		t.Errorf("warning should contain stability marker, got: %s", msg)
	}
	if !strings.Contains(msg, "net-negative") {
		t.Errorf("warning should mention net-negative, got: %s", msg)
	}
}

func TestSelfCorrectionGate_NoWarningWhenNetPositive(t *testing.T) {
	g := newSelfCorrectionGateState()

	// Simulate 3 rounds where each resolves 2 errors and introduces 0-1 new.
	// ECR >> EIR, ratio well above threshold.
	g.recordRound(1, 2, 2)        // round 1
	g.recordRound(0, 1, 2)        // round 2
	msg := g.recordRound(1, 1, 2) // round 3

	if msg != "" {
		t.Fatalf("expected no warning for net-positive self-correction, got: %s", msg)
	}
}

func TestSelfCorrectionGate_FiresOnlyOnce(t *testing.T) {
	g := newSelfCorrectionGateState()

	// Establish net-negative pattern.
	g.recordRound(2, 1, 0)
	g.recordRound(2, 2, 0)
	msg1 := g.recordRound(2, 2, 0)
	if msg1 == "" {
		t.Fatal("expected first warning")
	}

	// Continue — should NOT fire again.
	msg2 := g.recordRound(2, 2, 0)
	if msg2 != "" {
		t.Fatalf("expected no second warning (fires once per run), got: %s", msg2)
	}
}

func TestSelfCorrectionGate_NoNewErrorsNeverFires(t *testing.T) {
	g := newSelfCorrectionGateState()

	// All persistent errors — no regressions introduced.
	for i := 0; i < 5; i++ {
		msg := g.recordRound(0, 3, 0)
		if msg != "" {
			t.Fatalf("round %d: expected no warning when no new errors, got: %s", i+1, msg)
		}
	}
}

func TestSelfCorrectionGate_Reset(t *testing.T) {
	g := newSelfCorrectionGateState()

	g.recordRound(2, 1, 0)
	g.recordRound(2, 2, 0)
	_ = g.recordRound(2, 2, 0) // fires
	if !g.fired {
		t.Fatal("expected gate to have fired")
	}

	g.reset()

	if g.fired || g.totalRounds != 0 || g.totalNewErrors != 0 {
		t.Fatal("reset did not clear state")
	}
}

func TestSelfCorrectionGate_BorderlineRatio(t *testing.T) {
	g := newSelfCorrectionGateState()

	// 3 rounds: total new=3, total resolved=3. ratio = 1.0 < 1.2 → should fire.
	g.recordRound(1, 1, 1)
	g.recordRound(1, 1, 1)
	msg := g.recordRound(1, 1, 1)

	if msg == "" {
		t.Fatal("expected warning when ratio (1.0) is below threshold (1.2)")
	}
}

func TestSelfCorrectionGate_SlightlyAboveThresholdNoFire(t *testing.T) {
	g := newSelfCorrectionGateState()

	// 3 rounds: total new=3, total resolved=5. ratio = 1.67 > 1.2 → stable.
	g.recordRound(1, 1, 2)
	g.recordRound(1, 1, 1)
	msg := g.recordRound(1, 1, 2)

	if msg != "" {
		t.Fatalf("expected no warning when ratio (1.67) is above threshold (1.2), got: %s", msg)
	}
}

func TestSelfCorrectionGate_NilSafe(t *testing.T) {
	var g *selfCorrectionGateState
	// Should not panic.
	msg := g.recordRound(1, 1, 1)
	if msg != "" {
		t.Fatalf("nil gate should return empty, got: %s", msg)
	}
	g.reset()
}

func TestClassifyErrorsWithTransition_Structure(t *testing.T) {
	v := newVerifyRegressionState()

	// Round 1: establish baseline with errors A, B.
	tr1, msg1 := v.classifyErrorsWithTransition([]string{"error A in foo.go", "error B in bar.go"})
	if msg1 != "" {
		t.Fatalf("first round should return empty summary (baseline), got: %s", msg1)
	}
	if len(tr1.newErrors) != 0 || len(tr1.persistentErrors) != 0 || tr1.resolvedCount != 0 {
		t.Fatalf("first round transition should be empty, got: %+v", tr1)
	}

	// Round 2: A persists, B resolved, C is new.
	tr2, msg2 := v.classifyErrorsWithTransition([]string{"error A in foo.go", "error C in baz.go"})
	if msg2 == "" {
		t.Fatal("second round should return non-empty summary")
	}
	if len(tr2.newErrors) != 1 {
		t.Errorf("expected 1 new error, got %d", len(tr2.newErrors))
	}
	if len(tr2.persistentErrors) != 1 {
		t.Errorf("expected 1 persistent error, got %d", len(tr2.persistentErrors))
	}
	if tr2.resolvedCount != 1 {
		t.Errorf("expected 1 resolved, got %d", tr2.resolvedCount)
	}
}
