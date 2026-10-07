package a2a

// Per-peer A2A outbound circuit breaker (sa-88, r33-C backlog).
//
// Research basis (machinelearningmastery.com "7 Agentic AI Trends to Watch
// in 2026", 2026-01-05, trend 4 "Governance and Security as Competitive
// Differentiators"): "security agents that detect anomalous agent
// behavior" - in a multi-agent A2A delegation graph, a DOWN remote peer
// is the anomaly with the highest blast radius.
//
// THE GAP (sa-88 verified, grep zero hits before this file): internal/a2a
// had no outbound failure isolation. client.rpc() retries per-request
// (retryRPCLater) but a peer that is down fails EVERY call with the full
// retry+timeout cost - a2a_remote delegations to a dead instance burn
// the complete pipeline each time, forming a failure storm across the
// delegation chain. The pattern is already proven twice in this codebase
// (internal/mcp/breaker.go sa-21, internal/swarm/teammate_breaker.go
// r455); the A2A outbound domain was the missing third leg.
//
// DESIGN (mirrors the mcp/swarm precedents):
//   - One breaker per peer BASE URL (process-wide registry): the state
//     must be shared across Client instances pointing at the same peer.
//   - Only INFRASTRUCTURE failures trip it: transport errors (Do/read),
//     HTTP failure statuses. A peer that ANSWERS - JSON-RPC application
//     errors, decode issues, ctx cancellation from OUR side - is working
//     and never trips the breaker (mcp breaker's semantic/infra split).
//   - CLOSED → (threshold consecutive infra failures) → OPEN → (cooldown)
//     → HALF-OPEN: the next real call IS the probe (synchronous domain:
//     every allowance maps to an actual RPC, so the swarm #3245
//     stranded-probe hazard does not apply); success closes, infra
//     failure re-opens with a fresh cooldown.
//   - Fast-fail message states what happened and why retries waste a
//     full round-trip, giving the delegating agent an actionable path.
//   - Zero LLM cost: deterministic counters, O(1) state.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// peerBreakerThreshold is the number of consecutive infrastructure
// failures before the peer circuit opens (3, matching the mcp/swarm
// breaker precedents: enough to ride out a transient hiccup).
const peerBreakerThreshold = 3

// peerBreakerCooldown is how long an OPEN circuit waits before the next
// call is admitted as the half-open probe. Var (not const) so tests can
// shrink it (breakerCooldown precedent, internal/mcp).
var peerBreakerCooldown = 60 * time.Second

type peerBreakerState uint8

const (
	peerBreakerClosed peerBreakerState = iota
	peerBreakerOpen
	peerBreakerHalfOpen
)

type peerBreaker struct {
	mu       sync.Mutex
	state    peerBreakerState
	failures int       // consecutive infra failures (CLOSED state)
	openedAt time.Time // when the circuit last opened / probe started
}

// allow reports whether a call to this peer may proceed. An OPEN circuit
// whose cooldown expired transitions to HALF-OPEN and admits exactly one
// call - that call IS the probe whose outcome closes or re-opens.
func (b *peerBreaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case peerBreakerClosed:
		return true
	case peerBreakerOpen:
		if time.Since(b.openedAt) >= peerBreakerCooldown {
			b.state = peerBreakerHalfOpen
			return true // this call is the probe
		}
		return false
	default: // peerBreakerHalfOpen: a probe is in flight
		return false
	}
}

// record accounts one call outcome. Success closes from any state; an
// infra failure increments (CLOSED), re-opens (HALF-OPEN probe failed),
// or is ignored (OPEN straggler failure).
func (b *peerBreaker) record(infraFailed bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case peerBreakerHalfOpen:
		if infraFailed {
			b.state = peerBreakerOpen
			b.openedAt = time.Now()
		} else {
			b.state = peerBreakerClosed
			b.failures = 0
		}
	case peerBreakerOpen:
		// Straggler result of a call that started before the circuit
		// opened: only a success can close an OPEN circuit early.
		if !infraFailed {
			b.state = peerBreakerClosed
			b.failures = 0
		}
	default: // CLOSED
		if infraFailed {
			b.failures++
			if b.failures >= peerBreakerThreshold {
				b.state = peerBreakerOpen
				b.openedAt = time.Now()
			}
		} else {
			b.failures = 0
		}
	}
}

// stateSnapshot exposes the state for tests/diagnostics.
func (b *peerBreaker) stateSnapshot() peerBreakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// peerBreakers is the process-wide registry keyed by peer base URL, so
// every Client instance pointing at the same remote agent shares one
// circuit (a2a_remote may rebuild clients; the peer's health does not).
var peerBreakers sync.Map // string -> *peerBreaker

func peerBreakerFor(baseURL string) *peerBreaker {
	if baseURL == "" {
		return nil
	}
	if v, ok := peerBreakers.Load(baseURL); ok {
		return v.(*peerBreaker)
	}
	b := &peerBreaker{}
	actual, _ := peerBreakers.LoadOrStore(baseURL, b)
	return actual.(*peerBreaker)
}

// resetPeerBreakers clears the registry (tests only).
func resetPeerBreakers() {
	peerBreakers.Range(func(k, _ any) bool {
		peerBreakers.Delete(k)
		return true
	})
}

// peerCircuitOpenError is the sentinel for a fast-failed call; it lets
// callers (and tests) distinguish "circuit open, do not retry now" from
// an actual transport failure.
type peerCircuitOpenError struct {
	Peer     string
	Method   string
	Cooldown time.Duration
}

func (e *peerCircuitOpenError) Error() string {
	return fmt.Sprintf(
		"a2a %s: peer circuit open for %s (recent infra failures); "+
			"retrying now wastes a full timeout round-trip - wait %v or pick another peer",
		e.Method, e.Peer, e.Cooldown)
}

// classifyRPCError decides whether an rpc() error is an INFRASTRUCTURE
// failure of the peer (trips the breaker) or not:
//   - nil, context.Canceled/DeadlineExceeded from our side: no verdict
//   - *JSONRPCError: the peer ANSWERED with an application error - the
//     peer is up (semantic failure, mcp breaker precedent)
//   - local marshal/unmarshal of the RESULT: the peer's bytes arrived
//   - transport (Do/read) and non-RPC HTTP status errors: peer down
func classifyRPCError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var rpcErr *JSONRPCError
	if errors.As(err, &rpcErr) {
		return false
	}
	msg := err.Error()
	for _, local := range []string{
		"marshal params",
		"marshal request",
		": decode:",
		"re-marshal result",
		"unmarshal result",
	} {
		if strings.Contains(msg, local) {
			return false
		}
	}
	return true
}
