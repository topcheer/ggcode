package im

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestIssue4608_UnboundChannelNoRetrySpam verifies Bug 1: an inbound message
// from a channel with no binding (ErrNoChannelBound - a normal routing
// verdict, e.g. a message.channels subscription seeing unbound channels) must
// NOT trigger the "session not ready, please retry" reply. Before the fix the
// bot replied to every ordinary message in any channel it was invited to,
// spamming the channel with a misleading prompt.
func TestIssue4608_UnboundChannelNoRetrySpam(t *testing.T) {
	var posts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&posts, 1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"ts":"1718000000.000001"}`))
	}))
	defer srv.Close()

	// A real Manager with an active session but no channel binding for the
	// adapter: HandleInbound returns ErrNoChannelBound for the unbound channel
	// (same-package test sets the private session field directly).
	mgr := NewManager()
	mgr.session = &SessionBinding{SessionID: "sess-1", Workspace: "ws-1"}
	a := &slackAdapter{
		name:       "test_4608",
		manager:    mgr,
		apiBase:    srv.URL,
		httpClient: srv.Client(),
		botToken:   "xoxb-test",
		seen:       make(map[string]time.Time),
		connected:  true,
	}

	a.handleMessage(context.Background(), map[string]any{
		"type": "message",
		"user": "U111",
		// Empty channel id: the pairing layer passes it through unconsumed
		// (no challenge is created for an idless channel) and HandleInbound
		// returns ErrNoChannelBound for the bindingless adapter - the exact
		// branch the fix makes silent. Before the fix this sent the
		// "session not ready, please retry" reply.
		"channel": "",
		"text":    "hello world",
		"ts":      "1718000000.000100",
	})

	if n := atomic.LoadInt32(&posts); n != 0 {
		t.Fatalf("chat.postMessage called %d times for ErrNoChannelBound; want 0 (silent debug, no channel spam)", n)
	}
}

// TestIssue4608_UnboundChannelStillDedupsKeys verifies the dedup table is
// still maintained for unbound-channel messages (dedup runs before binding
// lookup), so the silent-drop path does not turn into a processing leak.
func TestIssue4608_UnboundChannelStillDedupsKeys(t *testing.T) {
	a := &slackAdapter{name: "test", seen: make(map[string]time.Time)}
	if !a.markSeenDedup("C_UNBOUND", "1718000000.000100") {
		t.Fatal("first delivery must be recorded")
	}
	if a.markSeenDedup("C_UNBOUND", "1718000000.000100") {
		t.Fatal("redelivery of the same (channel, ts) must be deduped even for unbound channels")
	}
}

// TestIssue4608_DedupBurstForcedEviction verifies Bug 2: when a burst within
// the 5-minute TTL window pushes the dedup map past slackDedupMaxSize, the
// oldest entries are forcibly evicted until the map is back under the cap
// (mirrors the mattermost adapter's #963 eviction loop). Before the fix the
// map was only TTL-evicted and could balloon to hundreds of thousands of
// entries under sustained >1000 msgs / 5 min traffic.
func TestIssue4608_DedupBurstForcedEviction(t *testing.T) {
	a := &slackAdapter{name: "test", seen: make(map[string]time.Time)}

	// Simulate a burst: slackDedupMaxSize+300 fresh entries, all within the
	// TTL window (staggered by milliseconds, not seconds, so the 5-minute
	// sweep removes nothing and only the forced eviction can bring the map
	// back under the cap).
	now := time.Now()
	burst := slackDedupMaxSize + 300
	for i := 0; i < burst; i++ {
		key := "C_BURST:" + time.Unix(int64(1718000000+i), 0).UTC().Format("150405.000000")
		a.seen[key] = now.Add(-time.Duration(burst-i) * time.Millisecond)
	}

	// One more delivery triggers the overflow path (TTL sweep removes nothing
	// because every entry is fresh; the forced eviction must bring us to cap).
	if !a.markSeenDedup("C_NEW", "1718009999.999999") {
		t.Fatal("new delivery must be processed")
	}

	a.mu.RLock()
	n := len(a.seen)
	_, newKeyAlive := a.seen["C_NEW:1718009999.999999"]
	a.mu.RUnlock()

	if n > slackDedupMaxSize {
		t.Errorf("seen table size = %d after burst, want <= %d (forced eviction missing)", n, slackDedupMaxSize)
	}
	if !newKeyAlive {
		t.Error("newest entry must survive eviction")
	}
	if n < slackDedupMaxSize {
		t.Errorf("seen table size = %d, want exactly %d (eviction must stop at the cap, not over-evict)", n, slackDedupMaxSize)
	}
}
