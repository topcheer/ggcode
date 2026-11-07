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

// #5200: node_id embeds pid+start-time, so every peer restart rotates it and
// every other seat's cached id goes stale. Stale-id DMs used to bounce
// ("no recipient found" or the #5117 stale refusal); senders had to re-list
// before every send. The fix: nick-first addressing that re-resolves at send
// time, plus (a) automatic rebind of a sender's stale cached id to the
// current holder of the same nick, (b) near-identical-nick guards around the
// techwriter22/techwriter222 mis-DM shape, (c) stale pre-restart twins no
// longer make nick addressing ambiguous.

// hub5200 seeds: one live HTTP peer (the restarted coordinator under its NEW
// node_id), plus an httptest counter so delivery is observable.
func hub5200(t *testing.T) (LanChatTool, *lanchat.Hub, *int32, *httptest.Server) {
	t.Helper()
	var delivered int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&delivered, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	tool, hub := newTestLanChatTool(t)
	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-new-5200",
		HumanNick: "techwriter_techwriter222",
		AgentNick: "techwriter_techwriter222",
		Endpoint:  srv.URL,
		Online:    true,
	})
	return tool, hub, &delivered, srv
}

// S-74 shape: sender DMs the peer's OLD node_id (cached before the peer
// restarted). The old entry is stale-but-not-deleted while the fresh
// registration (same nick, new node_id) is live. The send must rebind to the
// fresh node_id and DELIVER - not bounce with "no recipient found", not
// refuse with the #5117 stale error, no list re-fetch required.
func TestIssue5200StaleNodeIDRebindsToFreshHolder(t *testing.T) {
	tool, hub, delivered, _ := hub5200(t)
	// Pre-restart entry: same nick, old node_id, dead endpoint, LastSeen
	// older than ageOffline (3m) but younger than peerDeleteAfter (10m).
	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-old-5200",
		HumanNick: "techwriter_techwriter222",
		AgentNick: "techwriter_techwriter222",
		Endpoint:  "http://localhost:1",
		Online:    true,
	})
	hub.SetPeerLastSeenForTest("node-old-5200", time.Now().Add(-4*time.Minute))

	res, err := tool.doSend(context.Background(), "status?", []string{"node-old-5200"}, false, "")
	if err != nil {
		t.Fatalf("doSend: %v", err)
	}
	if res.IsError {
		t.Fatalf("stale-id send must rebind and deliver, got error: %s", res.Content)
	}
	for _, marker := range []string{"no recipient found", "stale recipient", "not sending"} {
		if strings.Contains(res.Content, marker) {
			t.Fatalf("rebind path must not produce %q, got: %s", marker, res.Content)
		}
	}
	if !strings.Contains(res.Content, "node-old-5200") || !strings.Contains(res.Content, "node-new-5200") {
		t.Fatalf("rebind report must name old and new node_ids, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "rebound") {
		t.Fatalf("rebind report must be explicit, got: %s", res.Content)
	}
	if n := atomic.LoadInt32(delivered); n != 1 {
		t.Fatalf("message must be delivered to the fresh node_id exactly once, got %d deliveries", n)
	}
}

// S-73 shape: both techwriter_techwriter22 and techwriter_techwriter222 are
// live peers (one edit apart). A sender who types the wrong one must SEE the
// danger, not deliver silently: successful nick sends carry a
// near-identical-nick warning, and a typo'd nick that matches nothing gets a
// did-you-mean hint instead of a bare bounce.
func TestIssue5200NearIdenticalNickGuards(t *testing.T) {
	tool, hub, delivered, srv := hub5200(t)
	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-22",
		HumanNick: "techwriter_techwriter22",
		AgentNick: "techwriter_techwriter22",
		Endpoint:  srv.URL,
		Online:    true,
	})

	// Exact nick send still delivers - and now carries the one-edit warning.
	res, err := tool.doSend(context.Background(), "hello", []string{"techwriter_techwriter22"}, false, "")
	if err != nil {
		t.Fatalf("doSend: %v", err)
	}
	if res.IsError {
		t.Fatalf("exact nick send must succeed, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Near-identical nick") || !strings.Contains(res.Content, "techwriter_techwriter222") {
		t.Fatalf("successful send to a one-edit-away nick must warn about the neighbor, got: %s", res.Content)
	}

	// One-char typo resolving to nothing: did-you-mean hint over the bare
	// no-recipient bounce.
	res, err = tool.doSend(context.Background(), "hello", []string{"techwriter_techwriter223"}, false, "")
	if err != nil {
		t.Fatalf("doSend: %v", err)
	}
	if !res.IsError {
		t.Fatalf("unresolvable typo must still be an error, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "no recipient found") {
		t.Fatalf("bounce must keep its no-recipient error, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Did you mean") {
		t.Fatalf("bounce must carry a did-you-mean hint, got: %s", res.Content)
	}
	// Only the first send above delivered.
	if n := atomic.LoadInt32(delivered); n != 1 {
		t.Fatalf("typo send must not deliver anywhere, got %d deliveries", n)
	}
}

// Restart makes the OLD registration stale while the fresh one (same nick)
// is live: nick addressing must resolve to the fresh holder instead of
// reporting the nick ambiguous (stale twins are ghosts, not recipients).
func TestIssue5200NickSurvivesPeerRestartTwin(t *testing.T) {
	tool, hub, delivered, _ := hub5200(t)
	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-old-5200",
		HumanNick: "techwriter_techwriter222",
		AgentNick: "techwriter_techwriter222",
		Endpoint:  "http://localhost:1",
		Online:    true,
	})
	hub.SetPeerLastSeenForTest("node-old-5200", time.Now().Add(-4*time.Minute))

	res, err := tool.doSend(context.Background(), "ping", []string{"techwriter_techwriter222"}, false, "")
	if err != nil {
		t.Fatalf("doSend: %v", err)
	}
	if res.IsError {
		t.Fatalf("nick send must survive a peer restart twin, got: %s", res.Content)
	}
	if strings.Contains(res.Content, "ambiguous") {
		t.Fatalf("stale twin must not make the nick ambiguous, got: %s", res.Content)
	}
	if n := atomic.LoadInt32(delivered); n != 1 {
		t.Fatalf("nick must resolve to the fresh holder exactly once, got %d deliveries", n)
	}
}

// Two LIVE peers sharing a nick keep the pre-existing ambiguity behavior:
// explicit error listing both node_ids, no coin-flip delivery. The fresh-only
// filter must not weaken genuine collision reporting.
func TestIssue5200GenuineNickCollisionStillAmbiguous(t *testing.T) {
	tool, hub, _, srv := hub5200(t)
	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-twin-live",
		HumanNick: "techwriter_techwriter222",
		AgentNick: "techwriter_techwriter222",
		Endpoint:  srv.URL,
		Online:    true,
	})

	res, err := tool.doSend(context.Background(), "hello", []string{"techwriter_techwriter222"}, false, "")
	if err != nil {
		t.Fatalf("doSend: %v", err)
	}
	if !res.IsError {
		t.Fatalf("two fresh holders of one nick must stay ambiguous, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "ambiguous") || !strings.Contains(res.Content, "node_id") {
		t.Fatalf("ambiguity error must point at node_id tie-break, got: %s", res.Content)
	}
}

// The prefix branch gets the same fresh-only treatment: a stale
// pre-restart entry whose nick extends the typed prefix must not shadow or
// duplicate the fresh holder.
func TestIssue5200PrefixResolutionIgnoresStaleTwin(t *testing.T) {
	tool, hub, delivered, _ := hub5200(t)
	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-old-5200",
		HumanNick: "techwriter_techwriter222",
		AgentNick: "techwriter_techwriter222",
		Endpoint:  "http://localhost:1",
		Online:    true,
	})
	hub.SetPeerLastSeenForTest("node-old-5200", time.Now().Add(-4*time.Minute))

	res, err := tool.doSend(context.Background(), "prefix ping", []string{"techwriter_techwriter2"}, false, "")
	if err != nil {
		t.Fatalf("doSend: %v", err)
	}
	if res.IsError {
		t.Fatalf("unique fresh prefix hit must resolve, got: %s", res.Content)
	}
	if strings.Contains(res.Content, "ambiguous") {
		t.Fatalf("stale twin must not make the prefix ambiguous, got: %s", res.Content)
	}
	if n := atomic.LoadInt32(delivered); n != 1 {
		t.Fatalf("prefix must resolve to the fresh holder exactly once, got %d deliveries", n)
	}
}

// Hub oracle: LookupPeerIDByNick resolves a nick to its unique fresh holder
// and refuses (ok=false) on zero or multiple fresh holders.
func TestIssue5200LookupPeerIDByNick(t *testing.T) {
	_, hub, _, _ := hub5200(t)

	if id, ok := hub.LookupPeerIDByNick("TechWriter_TechWriter222"); !ok || id != "node-new-5200" {
		t.Fatalf("nick must resolve case-insensitively to the fresh holder, got %q ok=%v", id, ok)
	}
	if _, ok := hub.LookupPeerIDByNick("nobody_here"); ok {
		t.Fatalf("unknown nick must not resolve")
	}

	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-second-live",
		HumanNick: "techwriter_techwriter222",
		AgentNick: "techwriter_techwriter222",
		Endpoint:  "http://localhost:2",
		Online:    true,
	})
	if _, ok := hub.LookupPeerIDByNick("techwriter_techwriter222"); ok {
		t.Fatalf("two fresh holders must not resolve to a coin-flip id")
	}
}
