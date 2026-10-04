package im

// #3326 probes: the echo-path counter must resolve and record under the
// manager lock. Regression 1 pins the locked lookup-by-message behavior
// (single + first-match semantics); regression 2 runs the echo against a
// concurrent locked deleter under -race - the old bare map range would
// trip the race detector.

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestIssue3326_RecordByMessageMatchesBinding(t *testing.T) {
	m := NewManager()
	m.currentBindings["a"] = &ChannelBinding{
		Workspace: "ws1", Adapter: "qq", ChannelID: "c1",
		LastInboundMessageID: "msg-7", PassiveReplyCount: 4,
	}
	if err := m.RecordPassiveReplyByMessage("msg-7", time.Now()); err != nil {
		t.Fatalf("record failed: %v", err)
	}
	if got := m.currentBindings["a"].PassiveReplyCount; got != 5 {
		t.Fatalf("count=%d, want 5", got)
	}
	if err := m.RecordPassiveReplyByMessage("no-such-msg", time.Now()); err != ErrNoChannelBound {
		t.Fatalf("missing message must return ErrNoChannelBound, got %v", err)
	}
}

func TestIssue3326_EchoRecordRacesWatcherDelete(t *testing.T) {
	m := NewManager()
	m.currentBindings["a"] = &ChannelBinding{
		Workspace: "ws1", Adapter: "qq", ChannelID: "c1",
		LastInboundMessageID: "msg-7", PassiveReplyCount: 0,
	}
	adapter, _ := newQQSendTestAdapter(t)
	adapter.manager = m

	var wg sync.WaitGroup
	const rounds = 50
	wg.Add(2)
	go func() { // echo path: recordEcho via sendReplyText success
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			_ = adapter.sendReplyText(context.Background(), "group-1", "msg-7", "echo")
		}
	}()
	go func() { // watcher-style locked delete + re-add churn
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			m.mu.Lock()
			delete(m.currentBindings, "a")
			m.currentBindings["a"] = &ChannelBinding{
				Workspace: "ws1", Adapter: "qq", ChannelID: "c1",
				LastInboundMessageID: "msg-7",
			}
			m.mu.Unlock()
		}
	}()
	wg.Wait()
}
