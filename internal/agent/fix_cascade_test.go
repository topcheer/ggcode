package agent

import (
	"testing"
)

func TestFixCascade_BasicThreshold(t *testing.T) {
	f := newFixCascadeState()

	// Cycle 1: edit -> verify -> fail
	f.recordEdit()
	g := f.recordVerify(true, true)
	if g != "" {
		t.Fatalf("expected no guidance at cycle 1, got: %s", g)
	}

	// Cycle 2: edit -> verify -> fail
	f.recordEdit()
	g = f.recordVerify(true, true)
	if g != "" {
		t.Fatalf("expected no guidance at cycle 2, got: %s", g)
	}

	// Cycle 3: edit -> verify -> fail -> threshold reached
	f.recordEdit()
	g = f.recordVerify(true, true)
	if g == "" {
		t.Fatal("expected guidance at cycle 3, got empty string")
	}
	if !cascadeContains(g, "HYPOTHESIS LOCK-IN") {
		t.Errorf("guidance should contain lock-in warning, got: %s", g)
	}
}

func TestFixCascade_SuccessResets(t *testing.T) {
	f := newFixCascadeState()

	// Two failed cycles
	f.recordEdit()
	f.recordVerify(true, true)
	f.recordEdit()
	f.recordVerify(true, true)

	// Successful verify resets the cascade
	f.recordEdit()
	g := f.recordVerify(true, false)
	if g != "" {
		t.Fatalf("expected no guidance after success, got: %s", g)
	}

	// Now need 3 more failed cycles to trigger
	f.recordEdit()
	g = f.recordVerify(true, true)
	if g != "" {
		t.Fatalf("expected no guidance at cycle 1 after reset, got: %s", g)
	}
}

func TestFixCascade_NoEditsNotCounted(t *testing.T) {
	f := newFixCascadeState()

	// Verify failures WITHOUT edits between them should not count as cascade.
	// This is a loop_detect / recurring_error issue, not a cascade.
	for i := 0; i < 5; i++ {
		f.recordVerify(false, true)
	}
	if f.failedVerifyCount != 0 {
		t.Errorf("expected 0 failed verify count without edits, got %d", f.failedVerifyCount)
	}
}

func TestFixCascade_FiresOncePerRun(t *testing.T) {
	f := newFixCascadeState()

	// Trigger threshold
	for i := 0; i < cascadeThreshold; i++ {
		f.recordEdit()
		f.recordVerify(true, true)
	}

	// Continue cycling - should not fire again
	f.recordEdit()
	g := f.recordVerify(true, true)
	if g != "" {
		t.Fatalf("expected no second guidance, got: %s", g)
	}
}

func TestFixCascade_Reset(t *testing.T) {
	f := newFixCascadeState()

	// Trigger threshold
	for i := 0; i < cascadeThreshold; i++ {
		f.recordEdit()
		f.recordVerify(true, true)
	}

	// Reset should clear state
	f.reset()
	if f.failedVerifyCount != 0 || f.guidanceFired || f.editCount != 0 {
		t.Error("reset did not clear state")
	}

	// Should be able to fire again after reset.
	// Cycle 1 after reset.
	f.recordEdit()
	g := f.recordVerify(true, true)
	if g != "" {
		t.Fatalf("expected no guidance at cycle 1 after reset, got: %s", g)
	}
	// Cycle 2 after reset.
	f.recordEdit()
	g = f.recordVerify(true, true)
	if g != "" {
		t.Fatalf("expected no guidance at cycle 2 after reset, got: %s", g)
	}
	// Cycle 3 triggers guidance.
	f.recordEdit()
	g = f.recordVerify(true, true)
	if g == "" {
		t.Error("expected guidance after reset and re-trigger")
	}
}

func TestCascadeGuidance(t *testing.T) {
	g := cascadeGuidance(4)
	if !cascadeContains(g, "4") {
		t.Error("guidance should mention the cycle count")
	}
	if !cascadeContains(g, "STOP") {
		t.Error("guidance should contain STOP directive")
	}
	if !cascadeContains(g, "ROOT CAUSE") {
		t.Error("guidance should mention root cause")
	}
}

func cascadeContains(s, substr string) bool {
	return len(s) >= len(substr) && cascadeIndexOf(s, substr) >= 0
}

func cascadeIndexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
