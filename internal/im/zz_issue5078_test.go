package im

// #5078: Start must assign a.server BEFORE autoBind publishes the channel
// binding. Once the binding is visible, manager Emit paths can call Send from
// any goroutine; the ordering guarantees every such Send observes a set
// server (happens-before via the manager's binding mutex), so no event is
// silently dropped and there is no unsynchronized write/read pair.

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestIssue5078_ServerReadyWhenBindingVisible(t *testing.T) {
	mgr, _ := testDummyManager()
	cfg := testDummyAdapterConfig(t)
	// Avoid touching the port file so shutdown stays clean in test.
	cfg.Extra["port_file"] = ""
	a := newDummyAdapter("eval", config.IMConfig{}, cfg, mgr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Start(ctx)

	// Wait until the autoBind-published binding is visible through the manager.
	bound := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, b := range mgr.CurrentBindings() {
			if b.Adapter == "eval" && b.ChannelID == "eval-channel" {
				bound = true
			}
		}
		if bound {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !bound {
		t.Fatal("binding never became visible; Start/autoBind failed")
	}

	// Core #5078 invariant: the moment the binding is observable, a.server
	// must already be assigned (fix orders newHTTPServer before autoBind).
	if a.server == nil {
		t.Fatal("binding visible but a.server is nil: events in the startup window would be dropped (race #5078)")
	}

	// And a Send issued right after binding visibility must deliver to the
	// SSE broker, proving the first post-bind event is not lost.
	ch, lastSeq := a.server.sseBroker.subscribe()
	defer a.server.sseBroker.unsubscribe(ch)
	err := a.Send(ctx, ChannelBinding{
		Workspace: "/tmp/test-workspace",
		Platform:  PlatformDummy,
		Adapter:   "eval",
		ChannelID: "eval-channel",
	}, OutboundEvent{Kind: OutboundEventText, Text: "first post-bind event"})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	select {
	case entry := <-ch:
		if entry.seq <= lastSeq {
			t.Fatalf("received stale entry seq=%d, want > %d", entry.seq, lastSeq)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first post-bind event never reached the SSE broker (dropped)")
	}
}
