package tui

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// TestEnsureIMRuntimeConcurrentWaiterGetsStarterResult pins #1417-A: a
// concurrent caller arriving while the starter is mid-flight used to get
// an unconditional nil - fake success - and then nil-derefed imManager on
// its next line. Waiters now block on done and observe the starter's real
// outcome, error included.
func TestEnsureIMRuntimeConcurrentWaiterGetsStarterResult(t *testing.T) {
	m := newTestModel()
	g := &imEnsureGuard{}
	m.imEnsure = g
	g.mu.Lock()
	g.starting = true
	g.done = make(chan struct{})
	startErr := errors.New("boom: adapter start failed")
	g.err = startErr
	g.mu.Unlock()

	// Waiter arrives mid-start: must NOT get nil.
	got := make(chan error, 1)
	go func() { got <- m.ensureStartedCurrentWorkspaceIMRuntime("unavail", "disabled", false) }()

	// Slight delay so the waiter is parked on done.
	time.Sleep(50 * time.Millisecond)
	g.mu.Lock()
	g.starting = false
	close(g.done)
	g.mu.Unlock()

	select {
	case err := <-got:
		if err == nil {
			t.Fatal("waiter got fake success while starter failed (#1417)")
		}
		if !strings.Contains(err.Error(), "boom") {
			t.Fatalf("waiter must receive the starter's error, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiter did not return (deadlock on done)")
	}
}
