package im

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// #1303: unsubscribe waited for the forwarder drain while holding the
// write lock, so a slow (or blocked) subscriber callback stalled every
// broadcastEvent - which sits on the agent's synchronous emit path.
// Now: detach+close under lock, drain outside it with a timeout.
func TestDaemonBridgeUnsubscribeDoesNotBlockBroadcast(t *testing.T) {
	// Subscribe/broadcastEvent only touch the sub registry - a bare
	// DaemonBridge suffices (same-package test).
	b := &DaemonBridge{}

	var entered atomic.Int32
	release := make(chan struct{})
	unsub := b.Subscribe(func(ev provider.StreamEvent) {
		entered.Add(1)
		<-release // simulate a slow consumer callback
	})

	// Queue an event; wait until the forwarder is INSIDE the callback
	// (deterministic - no sleep race), then start unsubscribing.
	b.broadcastEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "x"})
	deadline := time.Now().Add(2 * time.Second)
	for entered.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if entered.Load() == 0 {
		t.Fatal("forwarder never entered the subscriber callback")
	}

	done := make(chan struct{})
	go func() {
		unsub()
		close(done)
	}()

	// While the subscriber callback is still blocked, a fresh broadcast
	// must complete promptly (it only needs RLock, which the detached
	// unsubscribe no longer holds while draining).
	broadcastDone := make(chan struct{})
	go func() {
		b.broadcastEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "y"})
		close(broadcastDone)
	}()
	select {
	case <-broadcastDone:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("broadcastEvent blocked while unsubscribe was draining a slow subscriber")
	}

	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("unsubscribe did not return after drain timeout/callback release")
	}
}
