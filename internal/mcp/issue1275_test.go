package mcp

// Regression test for GitHub issue #1275: on a hung stdio server (process
// alive, pipe unresponsive) under sustained concurrent traffic, request
// timeouts never saw themselves as the last waiter, so Abort never fired,
// parked read goroutines accumulated, and the connection never healed.
// The hang watchdog must abort the connection after the grace window passes
// with zero read progress while waiters remain.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

var issue1275IDCounter atomic.Int64

func newTestRequestID() *ID {
	n := issue1275IDCounter.Add(1)
	id := NewIntID(n)
	return &id
}

func TestHungStdioServerWatchdogAborts(t *testing.T) {
	if testing.Short() {
		t.Skip("watchdog grace makes this test slow")
	}
	// `sleep 30` is the perfect hung server: alive, never reads stdin,
	// never writes stdout.
	c := NewClient("hung", "sleep", []string{"30"})
	if err := c.Start(context.Background()); err != nil {
		t.Skipf("cannot start sleep command: %v", err)
	}
	t.Cleanup(func() { c.Abort() })

	// Three overlapping waiters with staggered timeouts:
	//   A (200ms) and B (200ms) time out while others are still registered
	//   - the pre-#1275 stuck state ("connection kept", Abort blocked).
	//   C keeps a waiter registered WELL PAST the watchdog grace so the
	//   eventual abort can only come from the watchdog, not from C being
	//   the last waiter timing out (#644 path).
	doneC := make(chan error, 1)
	go func() {
		ctxC, cancel := context.WithTimeout(context.Background(), hungServerAbortGrace+15*time.Second)
		defer cancel()
		_, err := c.readResponseWithCancel(ctxC, newTestRequestID())
		doneC <- err
	}()

	ctxA, cancelA := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancelA()
	if _, err := c.readResponseWithCancel(ctxA, newTestRequestID()); err == nil {
		t.Fatal("request A must time out against the hung server")
	}

	ctxB, cancelB := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancelB()
	if _, err := c.readResponseWithCancel(ctxB, newTestRequestID()); err == nil {
		t.Fatal("request B must time out against the hung server")
	}

	// A/B timeouts armed the watchdog. The abort must arrive from the
	// watchdog (C is still waiting), healing the connection.
	deadline := time.Now().Add(hungServerAbortGrace + 5*time.Second)
	for time.Now().Before(deadline) {
		if c.closed.Load() {
			// C's read goroutine unwinds via the aborted pipe shortly after.
			select {
			case err := <-doneC:
				if err == nil {
					t.Fatal("waiter C must observe an error after the abort")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("waiter C did not unwind after abort")
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("hung connection was not aborted within %v despite persistent waiters", hungServerAbortGrace)
}

// TestHangAbortSignalsProcessExit pins the #1275 review fix: a watchdog
// abort must close processExit so the plugin reconnect watcher can restore
// service, while a user-initiated Abort stays silent (pre-existing design).
func TestHangAbortSignalsProcessExit(t *testing.T) {
	c := NewClient("hang-exit", "sleep", []string{"30"})
	if err := c.Start(context.Background()); err != nil {
		t.Skipf("cannot start sleep command: %v", err)
	}

	// Simulate the watchdog path: hang flag first, then Abort.
	c.hangAbort.Store(true)
	c.Abort()

	select {
	case <-c.ProcessExit():
		// Watchdog teardown surfaced to reconnect watchers.
	case <-time.After(5 * time.Second):
		t.Fatal("hang-watchdog abort must close processExit for reconnect watchers")
	}
}

// TestUserAbortStaysSilent pins the counter-semantics: a plain (user)
// Abort must NOT close processExit - reconnect watchers only react to
// unexpected or watchdog-initiated exits.
func TestUserAbortStaysSilent(t *testing.T) {
	c := NewClient("user-abort", "sleep", []string{"30"})
	if err := c.Start(context.Background()); err != nil {
		t.Skipf("cannot start sleep command: %v", err)
	}
	c.Abort()

	select {
	case <-c.ProcessExit():
		t.Fatal("user Abort must not close processExit (reconnect would fight deliberate teardown)")
	case <-time.After(1500 * time.Millisecond):
		// Silent as designed.
	}
}
