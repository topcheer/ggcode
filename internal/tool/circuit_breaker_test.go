package tool

import (
	"errors"
	"testing"
	"time"
)

func TestCircuitBreaker_Allow_Closed(t *testing.T) {
	cb := NewCircuitBreaker("test")

	if !cb.Allow() {
		t.Error("expected Allow() to return true in CLOSED state")
	}
}

func TestCircuitBreaker_Allow_AfterFailures(t *testing.T) {
	cb := NewCircuitBreaker("test")
	cb.maxFailures = 3 // Lower threshold for testing

	// Record failures to trip the circuit.
	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}

	// Circuit should be OPEN now.
	if cb.State() != StateOpen {
		t.Errorf("expected state OPEN, got %s", cb.State())
	}

	// Allow should return false.
	if cb.Allow() {
		t.Error("expected Allow() to return false in OPEN state")
	}
}

func TestCircuitBreaker_Allow_HalfOpenAfterReset(t *testing.T) {
	cb := NewCircuitBreaker("test")
	cb.maxFailures = 3
	cb.resetTimeout = 100 * time.Millisecond // Short timeout for testing

	// Trip the circuit.
	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}

	// Wait for reset timeout.
	time.Sleep(150 * time.Millisecond)

	// Should transition to HALF_OPEN and allow one probe.
	if !cb.Allow() {
		t.Error("expected Allow() to return true in HALF_OPEN state for probe")
	}

	if cb.State() != StateHalfOpen {
		t.Errorf("expected state HALF_OPEN, got %s", cb.State())
	}
}

func TestCircuitBreaker_RecordSuccess_ResetsCircuit(t *testing.T) {
	cb := NewCircuitBreaker("test")
	cb.maxFailures = 3
	cb.resetTimeout = 50 * time.Millisecond

	// Trip the circuit.
	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}

	// Wait and allow probe.
	time.Sleep(60 * time.Millisecond)
	cb.Allow()

	// Record success should reset to CLOSED.
	cb.RecordSuccess()

	if cb.State() != StateClosed {
		t.Errorf("expected state CLOSED after success, got %s", cb.State())
	}

	if cb.Failures() != 0 {
		t.Errorf("expected failures reset to 0, got %d", cb.Failures())
	}
}

func TestCircuitBreaker_RecordFailure_InHalfOpen(t *testing.T) {
	cb := NewCircuitBreaker("test")
	cb.maxFailures = 3
	cb.resetTimeout = 50 * time.Millisecond

	// Trip the circuit.
	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}

	// Wait and transition to HALF_OPEN.
	time.Sleep(60 * time.Millisecond)
	cb.Allow()
	if cb.State() != StateHalfOpen {
		t.Errorf("expected state HALF_OPEN, got %s", cb.State())
	}

	// Record failure in HALF_OPEN should go back to OPEN.
	cb.RecordFailure()

	if cb.State() != StateOpen {
		t.Errorf("expected state OPEN after failure in HALF_OPEN, got %s", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenLimit(t *testing.T) {
	cb := NewCircuitBreaker("test")
	cb.maxFailures = 3
	cb.resetTimeout = 50 * time.Millisecond
	cb.halfOpenLimit = 2 // Allow 2 probes.

	// Trip the circuit.
	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}

	// Wait and transition to HALF_OPEN.
	time.Sleep(60 * time.Millisecond)

	// Should allow exactly 2 probes.
	for i := 0; i < 2; i++ {
		if !cb.Allow() {
			t.Errorf("expected probe %d to be allowed", i+1)
		}
	}

	// Third probe should be rejected.
	if cb.Allow() {
		t.Error("expected third probe to be rejected after halfOpenLimit")
	}
}

func TestCircuitBreaker_StateString(t *testing.T) {
	tests := []struct {
		state    State
		expected string
	}{
		{StateClosed, "CLOSED"},
		{StateOpen, "OPEN"},
		{StateHalfOpen, "HALF_OPEN"},
	}

	for _, tt := range tests {
		if got := tt.state.String(); got != tt.expected {
			t.Errorf("State(%d).String() = %s, want %s", tt.state, got, tt.expected)
		}
	}
}

func TestCircuitBreakerError_Error(t *testing.T) {
	err := &CircuitOpenError{
		ToolName:      "test_tool",
		NextAttemptAt: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
	}

	msg := err.Error()
	if msg == "" {
		t.Error("error message should not be empty")
	}
}

func TestCircuitBreakerRegistry_GetOrCreate(t *testing.T) {
	reg := NewCircuitBreakerRegistry()

	cb1 := reg.GetOrCreate("tool1")
	cb2 := reg.GetOrCreate("tool1")

	if cb1 != cb2 {
		t.Error("expected same circuit breaker instance for same tool name")
	}

	cb3 := reg.GetOrCreate("tool2")
	if cb3 == cb1 {
		t.Error("expected different circuit breaker for different tool names")
	}
}

func TestCircuitBreakerRegistry_GetAllStates(t *testing.T) {
	reg := NewCircuitBreakerRegistry()

	// Create a few breakers.
	cb1 := reg.GetOrCreate("tool1")
	_ = reg.GetOrCreate("tool2") // Register but don't use

	// Trip one breaker.
	for i := 0; i < 5; i++ {
		cb1.RecordFailure()
	}

	states := reg.GetAllStates()

	if len(states) != 2 {
		t.Errorf("expected 2 states, got %d", len(states))
	}

	if states["tool1"] != StateOpen {
		t.Errorf("expected tool1 state OPEN, got %s", states["tool1"])
	}

	if states["tool2"] != StateClosed {
		t.Errorf("expected tool2 state CLOSED, got %s", states["tool2"])
	}
}

func TestIsCircuitOpenError(t *testing.T) {
	err := &CircuitOpenError{ToolName: "test"}
	var target *CircuitOpenError
	if !errors.As(err, &target) {
		t.Error("expected error to be CircuitOpenError")
	}

	otherErr := errors.New("some other error")
	var target2 *CircuitOpenError
	if errors.As(otherErr, &target2) {
		t.Error("expected non-circuit error to not be CircuitOpenError")
	}
}
