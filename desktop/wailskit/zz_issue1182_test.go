package wailskit

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// #1182: emit must not hold b.mu while the tunnel push runs. Before the fix
// the push ran under b.mu and could block on the broker's projection-sync
// wait, which needs b.mu on the reconnect snapshot path (CurrentTunnelStatus)
// - an AB-BA deadlock that froze the desktop including Cancel.
func TestEmitDoesNotHoldBridgeMutexDuringTunnelPush(t *testing.T) {
	release := make(chan struct{})
	b := &ChatBridge{}
	b.tunnelPush = func(_ provider.StreamEvent) {
		<-release // simulates a blocked projection-sync wait
	}

	done := make(chan struct{})
	go func() {
		b.emit(provider.StreamEvent{Type: provider.StreamEventToolCallChunk})
		close(done)
	}()

	// While the push is blocked, b.mu MUST still be acquirable (this is the
	// lock the mobile reconnect snapshot path and Cancel need).
	acquired := make(chan struct{})
	go func() {
		b.mu.Lock()
		b.mu.Unlock()
		close(acquired)
	}()
	select {
	case <-acquired:
		// good: mutex is free during the push
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("b.mu not acquirable while tunnel push blocked: emit still holds the lock (#1182 AB-BA)")
	}

	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("emit did not complete after push released")
	}
}

// #1182: the default push path (tunnelHost, no test hook) must still fire.
func TestEmitTunnelPushFallsBackToHost(t *testing.T) {
	b := &ChatBridge{}
	done := make(chan struct{})
	go func() {
		b.emit(provider.StreamEvent{Type: provider.StreamEventToolCallChunk})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("emit blocked on host tunnel push")
	}
}
