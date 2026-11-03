package im

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestDaemonBridgeUnsubscribeDoesNotBlockBroadcast(t *testing.T) {

	b := &DaemonBridge{}

	var entered atomic.Int32
	release := make(chan struct{})
	unsub := b.Subscribe(func(ev provider.StreamEvent) {
		entered.Add(1)
		<-release
	})

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
