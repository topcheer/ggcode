//go:build goolm

package mcp

// #3747 + #3748 probes.
//
// #3748: Abort() runs on timer/watchdog goroutines while Start()/Close()
// write the four transport-resource fields under c.mu. Abort's reads were
// lockless - go test -race reported DATA RACE. The resMu snapshot pattern
// makes every access race-free from any lock context.
//
// #3747: a response that arrives at the deadline instant was discarded by
// the ctx.Err() re-check (and could still lose the select when both
// channels were ready); the server had already executed the possibly
// non-idempotent tool and callers retried. Now an arrived result always
// wins.

import (
	"context"
	"encoding/json"
	"os/exec"
	"sync"
	"testing"
)

func TestIssue3748_AbortVsTransportWritersNoRace(t *testing.T) {
	c := NewClient("race-3748", "/bin/cat", nil)
	var wg sync.WaitGroup

	// Goroutine A: Abort from a timer/watchdog context (lockless caller).
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.Abort()
	}()

	// Goroutine B: Close-shaped writer (nil-out under no held lock).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			c.setTransportProcess(nil, nil, nil)
		}
	}()

	// Goroutine C: Start-shaped writer + snapshot reader loop.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			c.setTransportProcess(exec.Command("true"), func() {}, nil)
			_ = c.snapshotTransport()
		}
	}()
	wg.Wait()
	// The race detector is the assertion: any unsynchronized access to the
	// four resMu fields fails this test with a DATA RACE report.
}

func TestIssue3747_LateResponseAtDeadlineIsReturned(t *testing.T) {
	c := NewClient("waiter-3747", "/bin/cat", nil)
	reqID := NewIntID(42)
	idVal := reqID
	waiter := make(chan *Response, 1)
	c.registerWaiter(&idVal, waiter)
	defer c.unregisterWaiter(&idVal, waiter)

	// Pre-feed the waiter so the read goroutine's first loop iteration
	// delivers to done with no transport; the caller ctx is ALREADY expired
	// when readResponseWithWaiter starts - the exact discard shape #3747
	// describes (the done-branch ctx.Err() re-check and the post-abort
	// select both used to throw the arrived result away).
	want := json.RawMessage("42")
	waiter <- &Response{ID: want, JSONRPC: "2.0"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // expired from the start
	got, err := c.readResponseWithWaiter(ctx, &idVal, waiter)
	if err != nil {
		t.Fatalf("arrived response must win over an expired ctx, got err: %v", err)
	}
	if got == nil || string(got.ID) != string(want) {
		t.Fatalf("arrived response must be returned, got %+v", got)
	}
}
