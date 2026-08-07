package agent

import (
	"strings"
	"testing"
)

func TestUngroundedReflection_NoToolCallsResets(t *testing.T) {
	u := newUngroundedReflectionState()
	// Two text-only iterations, then a tool call resets
	u.recordIteration(1, false, 500)
	u.recordIteration(2, false, 500)
	msg := u.recordIteration(3, true, 100)
	if msg != "" {
		t.Fatalf("expected no warning when tool call present, got: %s", msg)
	}
	if u.consecutiveTextOnly != 0 {
		t.Fatalf("expected consecutiveTextOnly=0 after tool call, got %d", u.consecutiveTextOnly)
	}
}

func TestUngroundedReflection_TriggersAtThreshold(t *testing.T) {
	u := newUngroundedReflectionState()
	var msg string
	for i := 1; i <= ugrThreshold; i++ {
		msg = u.recordIteration(i, false, 500)
	}
	if msg == "" {
		t.Fatal("expected warning at threshold, got empty")
	}
	if !strings.Contains(msg, "Ungrounded Reflection") {
		t.Fatalf("expected message to contain header, got: %s", msg)
	}
	if !strings.Contains(msg, "ACT NOW") {
		t.Fatalf("expected actionable guidance, got: %s", msg)
	}
}

func TestUngroundedReflection_DoesNotTriggerBelowThreshold(t *testing.T) {
	u := newUngroundedReflectionState()
	for i := 1; i < ugrThreshold; i++ {
		msg := u.recordIteration(i, false, 500)
		if msg != "" {
			t.Fatalf("expected no warning below threshold at iter %d, got: %s", i, msg)
		}
	}
}

func TestUngroundedReflection_ShortTextIgnored(t *testing.T) {
	u := newUngroundedReflectionState()
	for i := 1; i <= ugrThreshold+2; i++ {
		msg := u.recordIteration(i, false, 50)
		if msg != "" {
			t.Fatalf("expected no warning for short text, got: %s", msg)
		}
	}
	if u.consecutiveTextOnly != 0 {
		t.Fatalf("expected consecutiveTextOnly=0 for short text, got %d", u.consecutiveTextOnly)
	}
}

func TestUngroundedReflection_MaxWarnings(t *testing.T) {
	u := newUngroundedReflectionState()
	warnings := 0
	for i := 1; i <= ugrThreshold+ugrCooldown+10; i++ {
		msg := u.recordIteration(i, false, 500)
		if msg != "" {
			warnings++
		}
	}
	if warnings > ugrMaxWarnings {
		t.Fatalf("expected max %d warnings, got %d", ugrMaxWarnings, warnings)
	}
}

func TestUngroundedReflection_CooldownBetweenWarnings(t *testing.T) {
	u := newUngroundedReflectionState()
	// First warning at iteration 3
	msg1 := u.recordIteration(1, false, 500)
	msg2 := u.recordIteration(2, false, 500)
	msg3 := u.recordIteration(3, false, 500)
	if msg1 != "" || msg2 != "" || msg3 == "" {
		t.Fatal("expected first warning at threshold=3")
	}
	// Should NOT fire immediately after (cooldown)
	msg4 := u.recordIteration(4, false, 500)
	if msg4 != "" {
		t.Fatalf("expected no warning during cooldown, got: %s", msg4)
	}
}

func TestUngroundedReflection_Reset(t *testing.T) {
	u := newUngroundedReflectionState()
	u.recordIteration(1, false, 500)
	u.recordIteration(2, false, 500)
	u.recordIteration(3, false, 500)
	if u.totalWarnings != 1 {
		t.Fatalf("expected 1 warning before reset, got %d", u.totalWarnings)
	}
	u.reset()
	if u.consecutiveTextOnly != 0 || u.totalWarnings != 0 || u.firedOnce {
		t.Fatal("reset did not clear state")
	}
}

func TestItoaUgr(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{1, "1"},
		{3, "3"},
		{10, "10"},
		{42, "42"},
		{-5, "-5"},
	}
	for _, tt := range tests {
		got := itoaUgr(tt.input)
		if got != tt.want {
			t.Errorf("itoaUgr(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
