package mcp

// Per-server MCP circuit breaker (sa-21).
//
// Research basis (2026 agentic-reliability engineering):
//   - "Building Resilient AI Agents: Error Handling, Retries and Circuit
//     Breakers" (TechMango, 2026) and "Agentic AI systems fail in storms:
//     one dead dependency, N wasted retries" (2026 agent-ops surveys) -
//     the canonical pattern is classify → trip → fast-fail → graceful
//     degrade (tell the agent WHAT failed and WHY, let it re-plan).
//
// THE GAP IN GGCODE: an MCP server that crashed or became unreachable
// fails EVERY call to EVERY tool it exposes. Before this breaker, each
// such call burned the full pipeline - often a 120s stdio request
// timeout or the agent's adaptive tool timeout - and the model would
// typically retry the same tool (or a sibling tool of the same dead
// server) several times, costing minutes of wall-clock time and tokens
// per outage before it gave up. recurring_error.go / error_compound.go
// only inject guidance text at the trajectory level; nothing at the
// execution layer short-circuits the wasted retries.
//
// DESIGN:
//   - One breaker per SERVER (not per tool): a dead server fails all of
//     its tools, and the breaker state must be shared across every
//     mcpTool instance, every agent clone (Registry.Clone shares the
//     pointer via Clone), and sibling tools.
//   - Only INFRASTRUCTURE failures trip it: connection refused/reset/
//     closed, EOF, broken pipe, DNS failure, i/o/deadline timeouts, TLS
//     failures, HTTP 5xx. Semantic failures (the server answered: JSON-
//     RPC "tool not found", "invalid params", or a tool result with
//     isError=true) are the server WORKING - they never trip the
//     breaker. Plain user-initiated context cancellation is also
//     excluded (Esc is not an outage).
//   - CLOSED → (threshold consecutive infra failures) → OPEN → (cooldown)
//     → HALF-OPEN probe: one call allowed; success closes, infra failure
//     reopens with a fresh cooldown.
//   - Fail-fast message follows the permissionDeniedMessage convention:
//     state what happened, why further retries waste a full LLM
//     round-trip, and give the agent an actionable recovery path.
//   - Zero LLM cost: deterministic counters, O(1) state.

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// breakerThreshold is the number of consecutive infrastructure
	// failures before the circuit opens. 3 balances against transient
	// hiccups (a single dropped WS frame) and against burning retries.
	breakerThreshold = 3

	// breakerCooldown is how long an OPEN circuit waits before allowing
	// one half-open probe call.
	breakerCooldown = 60 * time.Second
)

type breakerState int

const (
	breakerClosed breakerState = iota
	breakerOpen
	breakerHalfOpen
)

// serverBreaker is the per-server circuit breaker shared by all mcpTool
// instances of one MCP server. All methods are goroutine-safe.
type serverBreaker struct {
	mu        sync.Mutex
	server    string
	state     breakerState
	failures  int // consecutive infra failures (closed) or current trip count
	lastErr   string
	openedAt  time.Time
	probing   bool // a half-open probe call is in flight
	threshold int
	cooldown  time.Duration
}

func newServerBreaker(server string) *serverBreaker {
	return &serverBreaker{
		server:    server,
		threshold: breakerThreshold,
		cooldown:  breakerCooldown,
	}
}

// gate decides whether a tool call may proceed. When blocked is true, msg
// is a ready-to-return fast-fail error content. A blocked call is
// instantaneous (no transport I/O), which is the entire point: it saves
// the full timeout + LLM round-trip the retry would have burned.
func (b *serverBreaker) gate() (blocked bool, msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case breakerClosed:
		return false, ""
	case breakerOpen:
		if time.Since(b.openedAt) >= b.cooldown {
			// Cooldown elapsed: this call becomes the half-open probe.
			b.state = breakerHalfOpen
			b.probing = true
			return false, ""
		}
		remain := b.cooldown - time.Since(b.openedAt)
		if remain < 0 {
			remain = 0
		}
		return true, b.fastFailMessage(fmt.Sprintf(
			"cooldown ends in ~%ds; the next call after that will be a single probe",
			int(remain.Seconds())))
	case breakerHalfOpen:
		if b.probing {
			return true, b.fastFailMessage(
				"a probe call is already in flight; await its result before retrying")
		}
		// Should not happen (half-open implies probing), but allow the call.
		b.probing = true
		return false, ""
	}
	return false, ""
}

// recordFailure registers one infrastructure failure. When the consecutive
// count reaches the threshold, the circuit opens (closed→open) or reopens
// (half-open probe failed → fresh cooldown).
func (b *serverBreaker) recordFailure(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	b.lastErr = errString(err)
	switch b.state {
	case breakerClosed:
		if b.failures >= b.threshold {
			b.state = breakerOpen
			b.openedAt = time.Now()
			b.probing = false
			debugLogBreaker(b, "OPEN", "%d consecutive infra failures (last: %s)", b.failures, b.lastErr)
		} else {
			debugLogBreaker(b, "failure", "%d/%d (last: %s)", b.failures, b.threshold, b.lastErr)
		}
	case breakerHalfOpen:
		// Probe failed: reopen with a fresh cooldown.
		b.state = breakerOpen
		b.openedAt = time.Now()
		b.probing = false
		debugLogBreaker(b, "REOPEN", "probe failed: %s", b.lastErr)
	case breakerOpen:
		// Redundant failure raced the trip; keep the earliest openedAt.
	}
}

// recordSuccess registers one successful (transport-level) call. A server
// semantic error (result.IsError) is still a SUCCESS for breaker purposes:
// the server answered, so it is reachable.
func (b *serverBreaker) recordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state != breakerClosed || b.failures > 0 {
		debugLogBreaker(b, "CLOSED", "recovered after %d failures", b.failures)
	}
	b.state = breakerClosed
	b.failures = 0
	b.lastErr = ""
	b.probing = false
}

// fastFailMessage renders the actionable guidance given to the model when
// the circuit is open. Mirrors permissionDeniedMessage conventions:
// what happened, why retrying wastes a full LLM round-trip, and what to
// do instead.
func (b *serverBreaker) fastFailMessage(next string) string {
	return fmt.Sprintf(
		"MCP server %q circuit breaker is OPEN after %d consecutive infrastructure failures "+
			"(last error: %q). All tool calls to this server are being fast-failed WITHOUT "+
			"transport attempts because each retry costs a full agent round-trip that "+
			"cannot succeed while the server is down. %s. Do NOT retry this tool now; "+
			"accomplish the task with other tools, or report the MCP server outage to "+
			"the user and ask them to restart/reconnect the server.",
		b.server, b.failures, b.lastErr, next)
}

// statusSnapshot is a read-only diagnostic view (tests / future TUI status).
type statusSnapshot struct {
	State   string
	Failure int
	LastErr string
}

func (b *serverBreaker) snapshot() statusSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	var s string
	switch b.state {
	case breakerClosed:
		s = "closed"
	case breakerOpen:
		s = "open"
	case breakerHalfOpen:
		s = "half-open"
	}
	return statusSnapshot{State: s, Failure: b.failures, LastErr: b.lastErr}
}

// debugLogBreaker emits a state-transition log; helper keeps call sites tidy.
func debugLogBreaker(b *serverBreaker, event, format string, args ...interface{}) {
	debug.Log("mcp", "breaker[%s]: %s: %s", b.server, event, fmt.Sprintf(format, args...))
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// infraErrorPatterns are transport-level failure signatures. Matching is
// lowercase substring against the wrapped error chain rendered by Error().
// This is intentionally conservative: anything NOT matched is treated as a
// semantic failure and never trips the breaker.
var infraErrorPatterns = []string{
	"connection refused",
	"connection reset",
	"connection closed",
	"broken pipe",
	"unexpected eof",
	" eof",
	"no such host",
	"host unreachable",
	"network unreachable",
	"network is unreachable",
	"i/o timeout",
	"deadline exceeded", // includes context deadline exceeded (server hang)
	"tls:",
	"x509",
	"http 5",
	"status 5",
	"server error",
	"transport is closing",
	"read goroutine did not return after abort",
	"pipe is being closed",
	"not connected",
	"connection timed out",
}

// isInfraError classifies a CallTool transport error. Semantic errors -
// the server responded with a JSON-RPC error or executed the tool and
// returned isError=true - must NOT trip the breaker. Plain user-initiated
// context cancellation ("context cancelled/canceled", ggcode's Esc path)
// matches NO infra pattern below, so no explicit exclusion is needed;
// transport aborts that EMBED a cancel ("...after abort: context canceled")
// still match infra patterns and are correctly counted as outages.
func isInfraError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, p := range infraErrorPatterns {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

// errNotConnected is recorded when the adapter's caller is nil (the server
// never connected). Treated as infrastructure: no tool on that server can
// work.
type errNotConnected struct{ server string }

func (e errNotConnected) Error() string {
	return fmt.Sprintf("mcp[%s]: caller not connected (server may have crashed or not started)", e.server)
}
