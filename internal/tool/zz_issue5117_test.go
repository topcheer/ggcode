package tool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/lanchat"
)

// #5117: a DM to a stale-but-cached node_id (registry entry whose LastSeen
// exceeds ageOffline but which is not deleted until peerDeleteAfter) used to
// burn the ~10s unicast ACK timeout per attempt with an error callers
// misdiagnose as a network transient. Sends to such ids must fail fast with
// a stale-node error (last-seen age + list-refresh hint) and make no unicast
// attempt. Fresh peers and never-known ids keep their existing behavior.

// staleHub5117 seeds one stale-to-be peer (dead endpoint) and one fresh peer
// backed by a live test HTTP server so successful delivery is observable.
func staleHub5117(t *testing.T) (LanChatTool, *lanchat.Hub, *int32) {
	t.Helper()
	var freshDelivered int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&freshDelivered, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	tool, hub := newTestLanChatTool(t)
	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-stale",
		HumanNick: "stale-peer",
		Endpoint:  "http://localhost:1",
		Online:    true,
	})
	// Fresh peer points at the live server: HandlePresence stamps
	// LastSeen=now, so it sits well inside ageOffline.
	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-fresh",
		HumanNick: "fresh-peer",
		Endpoint:  srv.URL,
		Online:    true,
	})
	return tool, hub, &freshDelivered
}

// requireStaleRefusal asserts the #5117 fail-fast error shape: an error
// result naming the stale id, carrying the last-seen age and the
// action='list' refresh hint, and none of the transport-failure markers a
// unicast attempt would leave behind.
func requireStaleRefusal5117(t *testing.T, res Result, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("doSend returned go error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("stale recipient must produce an error result, got: %s", res.Content)
	}
	for _, want := range []string{"stale", "node-stale", "last seen", "action='list'", "not sending"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("stale error must mention %q, got: %s", want, res.Content)
		}
	}
	for _, marker := range []string{"tcp failed", "timeout", "failed to send to any recipient", "no recipient found"} {
		if strings.Contains(res.Content, marker) {
			t.Fatalf("stale refusal must not carry transport marker %q (unicast was attempted?), got: %s", marker, res.Content)
		}
	}
}

// Old-but-not-deleted entry (Online=true, LastSeen older than ageOffline but
// younger than peerDeleteAfter): the exact sweep-lag window from the report.
// Must fail fast with NO unicast attempt.
func TestIssue5117StaleRecipientFailsFastNoUnicast(t *testing.T) {
	tool, hub, delivered := staleHub5117(t)
	hub.SetPeerLastSeenForTest("node-stale", time.Now().Add(-4*time.Minute)) // ageOffline=3m, peerDeleteAfter=10m

	start := time.Now()
	res, err := tool.doSend(context.Background(), "hello", []string{"node-stale"}, false, "")
	requireStaleRefusal5117(t, res, err)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("stale send must fail fast (no unicast attempt; issue asks <100ms), took %s", d)
	} else if d >= 100*time.Millisecond {
		t.Logf("note: stale send took %s (issue asks <100ms)", d)
	}
	if n := atomic.LoadInt32(delivered); n != 0 {
		t.Fatalf("no delivery must be attempted for a stale-only send, got %d hits on the only live endpoint", n)
	}
}

// Sweep-lagged Offline flag with old LastSeen must also fail fast (this is
// the shape the merged warn-only #5120 fix handled; fail-fast must cover it).
func TestIssue5117OfflineFlagStaleFailsFast(t *testing.T) {
	tool, hub, _ := staleHub5117(t)
	hub.SetPeerOfflineForTest("node-stale", time.Now().Add(-2*time.Hour))

	start := time.Now()
	res, err := tool.doSend(context.Background(), "hello", []string{"node-stale"}, false, "")
	requireStaleRefusal5117(t, res, err)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("stale send must fail fast, took %s", d)
	}
}

// Mixed recipients: stale id is skipped with a report line, fresh id still
// gets the message (send proceeds for the fresh peer and actually arrives).
func TestIssue5117MixedSkipsStaleSendsFresh(t *testing.T) {
	tool, hub, delivered := staleHub5117(t)
	hub.SetPeerLastSeenForTest("node-stale", time.Now().Add(-4*time.Minute))

	res, err := tool.doSend(context.Background(), "hello", []string{"node-stale", "node-fresh"}, false, "")
	if err != nil {
		t.Fatalf("doSend: %v", err)
	}
	if res.IsError {
		t.Fatalf("mixed send with a fresh peer must proceed, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Stale/offline recipients skipped") {
		t.Fatalf("mixed send must report the skipped stale id, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "node-stale") || !strings.Contains(res.Content, "last seen") {
		t.Fatalf("skip report must name the stale id and its last-seen age, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "fresh-peer") {
		t.Fatalf("mixed send must still deliver to the fresh peer, got: %s", res.Content)
	}
	if n := atomic.LoadInt32(delivered); n != 1 {
		t.Fatalf("fresh peer must receive exactly one delivery, got %d", n)
	}
}

// Fresh entry (LastSeen within ageOffline): send proceeds and delivers, no
// stale error or warning.
func TestIssue5117FreshRecipientSends(t *testing.T) {
	tool, _, delivered := staleHub5117(t)
	res, err := tool.doSend(context.Background(), "hello", []string{"node-fresh"}, false, "")
	if err != nil {
		t.Fatalf("doSend: %v", err)
	}
	if res.IsError {
		t.Fatalf("fresh recipient must send, got: %s", res.Content)
	}
	if strings.Contains(res.Content, "stale") || strings.Contains(res.Content, "OFFLINE") {
		t.Fatalf("fresh send must not carry stale/OFFLINE warnings, got: %s", res.Content)
	}
	if n := atomic.LoadInt32(delivered); n != 1 {
		t.Fatalf("fresh peer must receive exactly one delivery, got %d", n)
	}
}

// Never-known id: unchanged 'no recipient found' path - not repackaged as a
// stale error. (The list dump appended to that error legitimately contains
// the fixture's node ids, so assert on the refusal markers instead.)
func TestIssue5117NeverKnownIDUnchanged(t *testing.T) {
	tool, _, _ := staleHub5117(t)
	res, err := tool.doSend(context.Background(), "hello", []string{"node-never-known"}, false, "")
	if err != nil {
		t.Fatalf("doSend: %v", err)
	}
	if !res.IsError {
		t.Fatalf("never-known id must error, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "no recipient found") {
		t.Fatalf("never-known id must keep the 'no recipient found' error, got: %s", res.Content)
	}
	if strings.Contains(res.Content, "not sending") || strings.Contains(res.Content, "last seen") {
		t.Fatalf("never-known id must not be reported as stale, got: %s", res.Content)
	}
}
