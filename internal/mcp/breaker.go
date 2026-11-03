package mcp

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	breakerThreshold = 3
)

var breakerCooldown = 60 * time.Second

type breakerState int

const (
	breakerClosed breakerState = iota
	breakerOpen
	breakerHalfOpen
)

type serverBreaker struct {
	mu        sync.Mutex
	server    string
	state     breakerState
	failures  int
	lastErr   string
	openedAt  time.Time
	probing   bool
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

func (b *serverBreaker) gate() (blocked bool, msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case breakerClosed:
		return false, ""
	case breakerOpen:
		if time.Since(b.openedAt) >= b.cooldown {

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

		b.probing = true
		return false, ""
	}
	return false, ""
}

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

		b.state = breakerOpen
		b.openedAt = time.Now()
		b.probing = false
		debugLogBreaker(b, "REOPEN", "probe failed: %s", b.lastErr)
	case breakerOpen:

	}
}

func (b *serverBreaker) abandonProbe() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state != breakerHalfOpen || !b.probing {
		return
	}
	b.probing = false
}

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

func debugLogBreaker(b *serverBreaker, event, format string, args ...interface{}) {
	debug.Log("mcp", "breaker[%s]: %s: %s", b.server, event, fmt.Sprintf(format, args...))
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

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
	"deadline exceeded",
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

func isInfraError(err error) bool {
	if err == nil {
		return false
	}

	if _, ok := jsonRPCErrorOf(err); ok {
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

type errNotConnected struct{ server string }

func (e errNotConnected) Error() string {
	return fmt.Sprintf("mcp[%s]: caller not connected (server may have crashed or not started)", e.server)
}
