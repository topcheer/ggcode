package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// #4155: the a2a refreshCache and lanchat syncPeers goroutines previously ran
// `for range ticker.C` with no exit path. refreshLoopUntil is the extracted
// shared loop; these tests pin the contract the fix depends on: cancellation
// actually stops the loop (goroutine exits, fn stops being called), the
// firstDelay window is cancellable, and firstDelay == 0 keeps the historical
// no-initial-call semantics of the refreshCache call site.

func TestRefreshLoopUntil_StopsOnCancel(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		refreshLoopUntil(ctx, 0, 5*time.Millisecond, func() { calls.Add(1) })
	}()

	// Let a few ticks fire.
	time.Sleep(60 * time.Millisecond)
	before := calls.Load()
	if before < 2 {
		t.Fatalf("expected at least 2 ticks before cancel, got %d", before)
	}

	cancel()
	select {
	case <-done:
		// Loop returned promptly: the goroutine has an exit path (#4155).
	case <-time.After(2 * time.Second):
		t.Fatal("refreshLoopUntil did not return after ctx cancel - loop still has no exit path")
	}

	// No further calls after cancel returned.
	time.Sleep(30 * time.Millisecond)
	if after := calls.Load(); after != before {
		t.Fatalf("fn called after cancel: before=%d after=%d", before, after)
	}
}

func TestRefreshLoopUntil_CancelDuringFirstDelaySkipsInitialCall(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		refreshLoopUntil(ctx, 10*time.Second, 10*time.Second, func() { calls.Add(1) })
	}()

	// Cancel well inside the firstDelay window.
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("refreshLoopUntil ignored cancellation during firstDelay")
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("initial call must not fire when cancelled during firstDelay, got %d calls", n)
	}
}

func TestRefreshLoopUntil_ZeroFirstDelayHasNoInitialCall(t *testing.T) {
	// Historical refreshCache semantics: with firstDelay == 0 the first fn
	// invocation happens at the first tick, not immediately.
	var mu sync.Mutex
	called := false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		refreshLoopUntil(ctx, 0, 50*time.Millisecond, func() {
			mu.Lock()
			called = true
			mu.Unlock()
		})
	}()

	// 10ms is inside the first 50ms tick window: fn must not have run yet.
	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	early := called
	mu.Unlock()
	if early {
		t.Fatal("fn invoked before first tick despite firstDelay == 0 - call-site semantics changed")
	}

	// But it must fire on the first tick.
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		c := called
		mu.Unlock()
		if c {
			break
		}
		select {
		case <-deadline:
			t.Fatal("fn never fired on first tick")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
}
