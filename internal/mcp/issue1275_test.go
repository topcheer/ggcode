package mcp

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

	c := NewClient("hung", "sleep", []string{"30"})
	if err := c.Start(context.Background()); err != nil {
		t.Skipf("cannot start sleep command: %v", err)
	}
	t.Cleanup(func() { c.Abort() })

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

	deadline := time.Now().Add(hungServerAbortGrace + 5*time.Second)
	for time.Now().Before(deadline) {
		if c.closed.Load() {

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

func TestHangAbortSignalsProcessExit(t *testing.T) {
	c := NewClient("hang-exit", "sleep", []string{"30"})
	if err := c.Start(context.Background()); err != nil {
		t.Skipf("cannot start sleep command: %v", err)
	}

	c.hangAbort.Store(true)
	c.Abort()

	select {
	case <-c.ProcessExit():

	case <-time.After(5 * time.Second):
		t.Fatal("hang-watchdog abort must close processExit for reconnect watchers")
	}
}

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

	}
}
