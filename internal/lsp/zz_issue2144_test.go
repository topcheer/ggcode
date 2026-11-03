package lsp

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAcquireRejectedAfterShutdownAll(t *testing.T) {
	m := &sessionManager{sessions: make(map[string]*sessionClient), stopCh: make(chan struct{})}
	m.shutdownAll()

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
	time.Sleep(20 * time.Millisecond)
	<-done

	if _, err := m.acquire(context.Background(), "/ws", ResolvedServer{Binary: "gopls"}); err == nil {
		t.Fatal("acquire after a completed shutdownAll must be rejected")
	}
}
