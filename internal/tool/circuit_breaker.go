package tool

import (
	"sync"
	"time"
)

// State represents the circuit breaker state (CLOSED, OPEN, HALF_OPEN).
type State int

const (
	// StateClosed allows normal operation.
	StateClosed State = iota
	// StateOpen blocks calls after threshold exceeded.
	StateOpen
	// StateHalfOpen allows limited calls to test recovery.
	StateHalfOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "CLOSED"
	case StateOpen:
		return "OPEN"
	case StateHalfOpen:
		return "HALF_OPEN"
	default:
		return "UNKNOWN"
	}
}

// CircuitBreaker implements the circuit breaker pattern for tool execution.
// It prevents cascading failures by blocking calls to tools that are failing.
//
// The three-state model:
//   - CLOSED: normal operation, calls pass through
//   - OPEN: failure threshold exceeded, calls immediately rejected
//   - HALF_OPEN: probe state, limited calls allowed to test recovery
//
// Research basis: Cordum 2026 production case study reports ~70% reduction
// in retry-storm incidents with shared circuit breakers in multi-agent systems.
type CircuitBreaker struct {
	name        string
	mu          sync.RWMutex
	state       State
	failures    int
	consecutive int // consecutive failures in current state

	// Thresholds
	maxFailures   int           // failures to trip to OPEN
	resetTimeout  time.Duration // how long to stay OPEN before HALF_OPEN
	halfOpenLimit int           // max calls in HALF_OPEN

	// Timing
	lastFailureTime time.Time
	nextAttemptTime time.Time
}

// NewCircuitBreaker creates a new circuit breaker with production-ready defaults.
//   - maxFailures: 5 failures trips the circuit
//   - resetTimeout: 30s before attempting recovery
//   - halfOpenLimit: 1 probe call before deciding
func NewCircuitBreaker(name string) *CircuitBreaker {
	return &CircuitBreaker{
		name:          name,
		state:         StateClosed,
		maxFailures:   5,
		resetTimeout:  30 * time.Second,
		halfOpenLimit: 1,
	}
}

// Allow returns true if a tool call should be allowed through the circuit.
// If false, the caller should fail fast with CircuitOpenError.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()

	// Auto-transition from OPEN to HALF_OPEN after reset timeout.
	if cb.state == StateOpen && now.After(cb.nextAttemptTime) {
		cb.state = StateHalfOpen
		cb.consecutive = 0
	}

	switch cb.state {
	case StateClosed:
		return true
	case StateHalfOpen:
		// Allow limited probe calls and track the count.
		if cb.consecutive < cb.halfOpenLimit {
			cb.consecutive++
			return true
		}
		return false
	case StateOpen:
		return false
	}
	return false
}

// RecordSuccess records a successful tool call.
// In HALF_OPEN state, a success transitions back to CLOSED.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == StateHalfOpen {
		// Probe succeeded, reset to CLOSED.
		cb.state = StateClosed
		cb.failures = 0
		cb.consecutive = 0
	}
}

// RecordFailure records a failed tool call.
// Too many failures trip the circuit to OPEN.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()
	cb.failures++
	// Only track consecutive failures in CLOSED state.
	// In HALF_OPEN, consecutive is tracked in Allow().
	if cb.state == StateClosed {
		cb.consecutive++
	}
	cb.lastFailureTime = now

	// Trip to OPEN if we exceed thresholds.
	if cb.state == StateHalfOpen {
		// Probe failed, stay in OPEN.
		cb.state = StateOpen
		cb.nextAttemptTime = now.Add(cb.resetTimeout)
	} else if cb.failures >= cb.maxFailures {
		// Threshold exceeded, trip to OPEN.
		cb.state = StateOpen
		cb.nextAttemptTime = now.Add(cb.resetTimeout)
	}
}

// State returns the current circuit breaker state.
func (cb *CircuitBreaker) State() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// Failures returns the total failure count.
func (cb *CircuitBreaker) Failures() int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.failures
}

// CircuitOpenError is returned when a tool call is rejected due to circuit breaker.
type CircuitOpenError struct {
	ToolName      string
	NextAttemptAt time.Time
}

func (e *CircuitOpenError) Error() string {
	return e.ToolName + ": circuit breaker OPEN, try again after " + e.NextAttemptAt.Format(time.RFC3339)
}

// CircuitBreakerRegistry manages circuit breakers for multiple tools.
// It ensures shared state across agent instances for consistent fault isolation.
type CircuitBreakerRegistry struct {
	mu  sync.RWMutex
	cbs map[string]*CircuitBreaker
}

// NewCircuitBreakerRegistry creates a new registry.
func NewCircuitBreakerRegistry() *CircuitBreakerRegistry {
	return &CircuitBreakerRegistry{
		cbs: make(map[string]*CircuitBreaker),
	}
}

// GetOrCreate returns a circuit breaker for the given tool name,
// creating it if it doesn't exist.
func (r *CircuitBreakerRegistry) GetOrCreate(toolName string) *CircuitBreaker {
	r.mu.RLock()
	cb, exists := r.cbs[toolName]
	r.mu.RUnlock()

	if exists {
		return cb
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after write lock.
	if cb, exists := r.cbs[toolName]; exists {
		return cb
	}

	cb = NewCircuitBreaker(toolName)
	r.cbs[toolName] = cb
	return cb
}

// GetAllStates returns a snapshot of all circuit breaker states for observability.
func (r *CircuitBreakerRegistry) GetAllStates() map[string]State {
	r.mu.RLock()
	defer r.mu.RUnlock()

	states := make(map[string]State, len(r.cbs))
	for name, cb := range r.cbs {
		states[name] = cb.State()
	}
	return states
}
