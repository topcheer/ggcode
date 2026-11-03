package agent

import (
	"testing"
)

func TestToolCallBudget_NoBudget(t *testing.T) {
	b := newToolCallBudget()

	for i := 0; i < 1000; i++ {
		b.record()
		msg, stop := b.check()
		if msg != "" || stop {
			t.Fatalf("expected no enforcement without budget, got msg=%q stop=%v at call %d", msg, stop, i+1)
		}
	}
}

func TestToolCallBudget_ExplicitBudget(t *testing.T) {
	b := newToolCallBudget()
	b.SetBudget(100)

	for i := 0; i < 79; i++ {
		b.record()
		msg, stop := b.check()
		if msg != "" || stop {
			t.Fatalf("unexpected message at call %d: msg=%q stop=%v", i+1, msg, stop)
		}
	}

	b.record()
	msg, stop := b.check()
	if msg == "" || stop {
		t.Fatalf("expected warning at 80%%, got msg=%q stop=%v", msg, stop)
	}

	b.record()
	msg, stop = b.check()
	if msg != "" || stop {
		t.Fatalf("expected no re-warning at 81%%, got msg=%q stop=%v", msg, stop)
	}

	for i := 82; i < 95; i++ {
		b.record()
		msg, _ = b.check()

		if b.warn95Given {
			t.Fatalf("95%% warning fired too early at call %d", i+1)
		}
		_ = msg
	}
	b.record()
	msg, stop = b.check()
	if msg == "" || stop {
		t.Fatalf("expected urgent warning at 95%%, got msg=%q stop=%v", msg, stop)
	}

	for i := 96; i < 100; i++ {
		b.record()
		b.check()
	}
	b.record()
	msg, stop = b.check()
	if msg == "" || !stop {
		t.Fatalf("expected hard stop at 100%%, got msg=%q stop=%v", msg, stop)
	}

	b.record()
	msg, stop = b.check()
	if msg != "" {
		t.Fatalf("expected no re-fire after stop, got msg=%q stop=%v", msg, stop)
	}
}

func TestToolCallBudget_DefaultBudget(t *testing.T) {
	b := newToolCallBudget()
	b.SetDefaultBudget(50)

	for i := 0; i < 39; i++ {
		b.record()
		b.check()
	}

	b.record()
	msg, _ := b.check()
	if msg == "" {
		t.Fatal("expected warning at 80% of default budget")
	}
}

func TestToolCallBudget_ExplicitOverridesDefault(t *testing.T) {
	b := newToolCallBudget()
	b.SetDefaultBudget(10)
	b.SetBudget(100)

	for i := 0; i < 9; i++ {
		b.record()
		msg, stop := b.check()

		if msg != "" {
			t.Fatalf("explicit budget should override default: got unexpected msg at call %d: %q", i+1, msg)
		}
		_ = stop
	}
}

func TestToolCallBudget_Reset(t *testing.T) {
	b := newToolCallBudget()
	b.SetBudget(10)

	for i := 0; i < 10; i++ {
		b.record()
		b.check()
	}
	if !b.stopGiven {
		t.Fatal("expected stop to have fired")
	}

	b.reset()
	if b.totalCalls != 0 || b.stopGiven || b.warn80Given || b.warn95Given {
		t.Fatal("reset did not clear state")
	}

	for i := 0; i < 8; i++ {
		b.record()
	}
	msg, _ := b.check()
	if msg == "" {
		t.Fatal("expected warning to fire after reset")
	}
}

func TestDeriveDefaultBudget(t *testing.T) {
	tests := []struct {
		maxIter int
		want    int
	}{
		{0, defaultToolCallBudgetUnlimited},
		{-1, defaultToolCallBudgetUnlimited},
		{10, 80},
		{25, 200},
		{50, 400},
	}
	for _, tt := range tests {
		got := deriveDefaultBudget(tt.maxIter)
		if got != tt.want {
			t.Errorf("deriveDefaultBudget(%d) = %d, want %d", tt.maxIter, got, tt.want)
		}
	}
}

func TestToolCallBudget_EffectiveBudget(t *testing.T) {
	b := newToolCallBudget()

	if eb := b.effectiveBudget(); eb != 0 {
		t.Errorf("expected 0, got %d", eb)
	}

	b.SetDefaultBudget(50)
	if eb := b.effectiveBudget(); eb != 50 {
		t.Errorf("expected 50, got %d", eb)
	}

	b.SetBudget(100)
	if eb := b.effectiveBudget(); eb != 100 {
		t.Errorf("expected 100 (explicit overrides), got %d", eb)
	}
}
