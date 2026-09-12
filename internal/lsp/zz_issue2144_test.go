package lsp

// #2144 regression: acquire had no closed check, so a quit-path tool call
// (quit does not cancel the agent ctx) racing ShutdownAll started a NEW
// server, inserted it into the already-drained map, and left it orphaned -
// shutdownOnce was spent, the reaper was stopped. The exit notification
// also shared the shutdown call's 2s deadline: a slow server consumed the
// budget and notify's ctx.Done short-circuit silently dropped exit,
// falling to Kill instead of the spec's graceful shutdown->exit sequence.

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAcquireRejectedAfterShutdownAll(t *testing.T) {
	m := &sessionManager{sessions: make(map[string]*sessionClient), stopCh: make(chan struct{})}
	m.shutdownAll()

	// An in-flight tool call arriving after the drain must be rejected,
	// not spawn an orphan server.
	_, err := m.acquire(context.Background(), "/ws", ResolvedServer{Binary: "gopls"})
	if err == nil {
		t.Fatal("acquire after shutdownAll must be rejected (was: orphan session)")
	}
	if !strings.Contains(err.Error(), "shut down") {
		t.Fatalf("rejection must be explicit, got: %v", err)
	}
}

func TestShutdownAllRejectsConcurrentAcquire(t *testing.T) {
	m := &sessionManager{sessions: make(map[string]*sessionClient), stopCh: make(chan struct{})}

	// Hold the manager lock to simulate an in-progress acquire critical
	// section, run shutdownAll (queues on the lock), then release:
	// shutdownAll must set closed in the same critical section as the
	// drain, so the next acquire is rejected.
	released := make(chan struct{})
	go func() {
		m.mu.Lock()
		close(released)
		<-time.After(50 * time.Millisecond)
		m.mu.Unlock()
	}()
	<-released
	done := make(chan struct{})
	go func() {
		m.shutdownAll()
		close(done)
	}()
	time.Sleep(20 * time.Millisecond) // let shutdownAll queue on the lock
	<-done

	if _, err := m.acquire(context.Background(), "/ws", ResolvedServer{Binary: "gopls"}); err == nil {
		t.Fatal("acquire after a completed shutdownAll must be rejected")
	}
}
