package wailskit

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestEmitDoesNotHoldBridgeMutexDuringTunnelPush(t *testing.T) {
	release := make(chan struct{})
	b := &ChatBridge{}
	b.tunnelPush = func(_ provider.StreamEvent) {
		<-release
	}

	done := make(chan struct{})
	go func() {
		b.emit(provider.StreamEvent{Type: provider.StreamEventToolCallChunk})
		close(done)
	}()

	acquired := make(chan struct{})
	go func() {
		b.mu.Lock()
		b.mu.Unlock()
		close(acquired)
	}()
	select {
	case <-acquired:

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
