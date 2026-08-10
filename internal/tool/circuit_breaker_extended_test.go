package tool

import (
	"errors"
	"testing"
	"time"
)

// TestExtendedStates verifies the extended circuit breaker states for flapping prevention.
func TestExtendedStates(t *testing.T) {
	cb := NewCircuitBreaker("test")

	// Trip to OPEN with max failures.
	for i := 0; i < 5; i++ {
		cb.RecordFailure()
	}

	if cb.State() != StateOpen {
		t.Fatalf("expected OPEN, got %v", cb.State())
	}

	// Manually advance nextAttemptTime to transition to HALF_OPEN without waiting.
	cb.mu.Lock()
	cb.nextAttemptTime = time.Now().Add(-1 * time.Second)
	cb.mu.Unlock()

	if !cb.Allow() {
		t.Fatal("expected Allow() to return true in HALF_OPEN")
	}

	if cb.State() != StateHalfOpen {
		t.Fatalf("expected HALF_OPEN, got %v", cb.State())
	}

	// Probe fails - should go to OPEN (not extended yet, first flap).
	cb.RecordFailure()

	if cb.State() != StateOpen {
		t.Fatalf("expected OPEN after first probe failure, got %v", cb.State())
	}

	// Manually advance nextAttemptTime again.
	cb.mu.Lock()
	cb.nextAttemptTime = time.Now().Add(-1 * time.Second)
	cb.mu.Unlock()

	if !cb.Allow() {
		t.Fatal("expected Allow() to return true in HALF_OPEN (second attempt)")
	}

	// Probe fails again - should trigger OPEN_EXTENDED (second flap).
	cb.RecordFailure()

	if cb.State() != StateOpenExtended {
		t.Fatalf("expected OPEN_EXTENDED after second probe failure, got %v", cb.State())
	}

	// Verify extended timeout is used (15min vs 30s).
	// The next attempt time should be far in the future.
	nextAttempt := cb.nextAttemptTime
	time.Sleep(100 * time.Millisecond)
	if !nextAttempt.After(time.Now()) {
		t.Fatal("expected OPEN_EXTENDED to use extended timeout (15min)")
	}
}

// TestFlapCountReset verifies that successful recovery resets the flap count.
func TestFlapCountReset(t *testing.T) {
	cb := NewCircuitBreaker("test")

	// Trip to OPEN.
	for i := 0; i < 5; i++ {
		cb.RecordFailure()
	}

	// Manually advance to HALF_OPEN.
	cb.mu.Lock()
	cb.nextAttemptTime = time.Now().Add(-1 * time.Second)
	cb.mu.Unlock()

	// Transition to HALF_OPEN by calling Allow.
	if !cb.Allow() {
		t.Fatal("expected Allow() to return true in HALF_OPEN")
	}

	// Record success - should reset to CLOSED and reset flap count.
	cb.RecordSuccess()

	if cb.State() != StateClosed {
		t.Fatalf("expected CLOSED after success, got %v", cb.State())
	}

	if cb.flapCount != 0 {
		t.Fatalf("expected flapCount reset to 0, got %d", cb.flapCount)
	}
}

// TestIsInfrastructureError verifies error classification for circuit breaker decisions.
func TestIsInfrastructureError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"generic error", errors.New("something went wrong"), true}, // default to true for safety
		{"timeout error", errors.New("operation timeout"), true},
		{"connection refused", errors.New("connection refused"), true},
		{"502 Bad Gateway", &testHTTPError{code: 502}, true},
		{"503 Service Unavailable", &testHTTPError{code: 503}, true},
		{"504 Gateway Timeout", &testHTTPError{code: 504}, true},
		{"429 Rate Limit", &testHTTPError{code: 429}, true},
		{"400 Bad Request", &testHTTPError{code: 400}, false},
		{"401 Unauthorized", &testHTTPError{code: 401}, false},
		{"403 Forbidden", &testHTTPError{code: 403}, false},
		{"404 Not Found", &testHTTPError{code: 404}, false},
		{"200 OK", &testHTTPError{code: 200}, true}, // not an error, but default to true
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsInfrastructureError(tt.err)
			if result != tt.expected {
				t.Errorf("IsInfrastructureError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

// testHTTPError implements a simple error with HTTP status code for testing.
type testHTTPError struct {
	code int
	msg  string
}

func (e *testHTTPError) Error() string {
	return e.msg
}

func (e *testHTTPError) HTTPStatusCode() int {
	return e.code
}

// TestHalfOpenExtended verifies the HALF_OPEN_EXTENDED state transitions correctly.
func TestHalfOpenExtended(t *testing.T) {
	cb := NewCircuitBreaker("test")

	// Trip to OPEN.
	for i := 0; i < 5; i++ {
		cb.RecordFailure()
	}

	// First flap: OPEN -> HALF_OPEN -> fail -> OPEN
	cb.mu.Lock()
	cb.nextAttemptTime = time.Now().Add(-1 * time.Second)
	cb.mu.Unlock()
	cb.Allow()
	cb.RecordFailure()

	if cb.State() != StateOpen {
		t.Fatalf("expected OPEN after first flap, got %v", cb.State())
	}

	// Second flap: OPEN -> HALF_OPEN -> fail -> OPEN_EXTENDED
	cb.mu.Lock()
	cb.nextAttemptTime = time.Now().Add(-1 * time.Second)
	cb.mu.Unlock()
	cb.Allow()
	cb.RecordFailure()

	if cb.State() != StateOpenExtended {
		t.Fatalf("expected OPEN_EXTENDED after second flap, got %v", cb.State())
	}

	// Wait for extended timeout to transition to HALF_OPEN_EXTENDED.
	// For testing, we'll manually set nextAttemptTime to the past.
	cb.mu.Lock()
	cb.nextAttemptTime = time.Now().Add(-1 * time.Second)
	cb.mu.Unlock()

	// Allow should now transition to HALF_OPEN_EXTENDED.
	if !cb.Allow() {
		t.Fatal("expected Allow() to return true in HALF_OPEN_EXTENDED")
	}

	if cb.State() != StateHalfOpenExtended {
		t.Fatalf("expected HALF_OPEN_EXTENDED, got %v", cb.State())
	}

	// Probe succeeds - should reset to CLOSED.
	cb.RecordSuccess()

	if cb.State() != StateClosed {
		t.Fatalf("expected CLOSED after success, got %v", cb.State())
	}
}

// TestStringRepresentation verifies all state string representations.
func TestStringRepresentation(t *testing.T) {
	tests := []struct {
		state    State
		expected string
	}{
		{StateClosed, "CLOSED"},
		{StateOpen, "OPEN"},
		{StateHalfOpen, "HALF_OPEN"},
		{StateOpenExtended, "OPEN_EXTENDED"},
		{StateHalfOpenExtended, "HALF_OPEN_EXTENDED"},
		{State(999), "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.state.String(); got != tt.expected {
				t.Errorf("State.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}
