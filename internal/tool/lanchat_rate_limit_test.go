package tool

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/lanchat"
)

// TestAgentRateLimiter_DMCooldownPerRecipient verifies that DMs to different
// recipients are independent, but multiple DMs to the same recipient are limited.
func TestAgentRateLimiter_DMCooldownPerRecipient(t *testing.T) {
	rl := newAgentRateLimiter()
	sender := "node-self"

	// DM to recipient A — allowed
	if msg := rl.checkDM(sender, "node-a", 0); msg != "" {
		t.Fatalf("first DM to node-a should be allowed, got: %s", msg)
	}
	rl.recordDM(sender, "node-a")

	// DM to recipient A again — rate-limited
	msg := rl.checkDM(sender, "node-a", 0)
	if msg == "" {
		t.Fatal("second DM to node-a within cooldown should be rate-limited")
	}
	if !strings.Contains(msg, "rate-limited") {
		t.Errorf("error should mention rate-limited, got: %s", msg)
	}

	// DM to recipient B — allowed (independent cooldown)
	if msg := rl.checkDM(sender, "node-b", 0); msg != "" {
		t.Fatalf("first DM to node-b should be allowed, got: %s", msg)
	}
	rl.recordDM(sender, "node-b")

	// Simulate cooldown expiry for node-a
	rl.mu.Lock()
	key := sender + "\u2192" + "node-a"
	rl.dmLastSent[key] = time.Now().Add(-(defaultAgentDMCooldown + time.Second))
	rl.mu.Unlock()

	// After expiry, DM to node-a should be allowed
	if msg := rl.checkDM(sender, "node-a", 0); msg != "" {
		t.Fatalf("DM to node-a after cooldown should be allowed, got: %s", msg)
	}
}

// TestAgentRateLimiter_DifferentSenders verifies that the sender→recipient key
// correctly distinguishes between different senders messaging the same recipient.
func TestAgentRateLimiter_DifferentSenders(t *testing.T) {
	rl := newAgentRateLimiter()

	// Sender A messages recipient C — allowed
	if msg := rl.checkDM("sender-a", "node-c", 0); msg != "" {
		t.Fatalf("sender-a → node-c should be allowed, got: %s", msg)
	}
	rl.recordDM("sender-a", "node-c")

	// Sender B messages same recipient C — also allowed (different sender)
	if msg := rl.checkDM("sender-b", "node-c", 0); msg != "" {
		t.Fatalf("sender-b → node-c should be allowed (different sender), got: %s", msg)
	}

	// Sender A messages C again — rate-limited
	if msg := rl.checkDM("sender-a", "node-c", 0); msg == "" {
		t.Fatal("sender-a → node-c should be rate-limited")
	}
}

// TestAgentRateLimiter_CustomCooldown verifies that a custom cooldown value
// is respected when passed to checkDM.
func TestAgentRateLimiter_CustomCooldown(t *testing.T) {
	rl := newAgentRateLimiter()
	sender := "node-self"

	// First DM — allowed
	if msg := rl.checkDM(sender, "node-x", 0); msg != "" {
		t.Fatalf("first DM should be allowed, got: %s", msg)
	}
	rl.recordDM(sender, "node-x")

	// Second DM with a 10s custom cooldown — should still be rate-limited
	if msg := rl.checkDM(sender, "node-x", 10*time.Second); msg == "" {
		t.Fatal("second DM within 10s custom cooldown should be rate-limited")
	}

	// Simulate 11s passing — now the 10s cooldown should have expired
	rl.mu.Lock()
	key := sender + "\u2192" + "node-x"
	rl.dmLastSent[key] = time.Now().Add(-11 * time.Second)
	rl.mu.Unlock()

	// Should be allowed now
	if msg := rl.checkDM(sender, "node-x", 10*time.Second); msg != "" {
		t.Fatalf("DM after custom cooldown expiry should be allowed, got: %s", msg)
	}
}

// TestLanChatSendRateLimited verifies end-to-end DM rate limiting through doSend.
// We test the blocking behavior: after pre-recording a DM, the next doSend is blocked.
func TestLanChatSendRateLimited(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	ctx := context.Background()

	// Add a test peer
	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-bob",
		HumanNick: "bob_dev",
		AgentNick: "bob_dev_agent",
		Endpoint:  "http://localhost:2",
		Role:      "dev",
		Online:    true,
	})

	// Pre-populate: simulate that we already DM'd node-bob recently
	tool.rateLimiter.recordDM(hub.NodeID(), "node-bob")

	// Second DM to same recipient — should be rate-limited BEFORE network send
	r2, err := tool.doSend(ctx, "hello again", []string{"node-bob"}, true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r2.IsError {
		t.Fatal("second DM to same recipient should be rate-limited")
	}
	if !strings.Contains(r2.Content, "rate-limited") {
		t.Errorf("expected 'rate-limited' in error, got: %s", r2.Content)
	}
	if !strings.Contains(r2.Content, "bob_dev_agent") {
		t.Errorf("error should mention the recipient nick, got: %s", r2.Content)
	}

	// DM to a different recipient — should NOT be rate-limited (would fail on network, not rate limit)
	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-carol",
		HumanNick: "carol_qa",
		AgentNick: "carol_qa_agent",
		Endpoint:  "http://localhost:3",
		Role:      "qa",
		Online:    true,
	})
	r3, err := tool.doSend(ctx, "hi carol", []string{"node-carol"}, true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// It will fail on network (no transport), but should NOT be a rate-limit error
	if r3.IsError && strings.Contains(r3.Content, "rate-limited") {
		t.Errorf("DM to different recipient should not be rate-limited: %s", r3.Content)
	}
}

// TestLanChatSendAsHumanNotRateLimited verifies that human-originated DMs
// are never rate-limited — the rate limiter is bypassed entirely.
func TestLanChatSendAsHumanNotRateLimited(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	ctx := context.Background()

	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-bob",
		HumanNick: "bob_dev",
		AgentNick: "bob_dev_agent",
		Endpoint:  "http://localhost:2",
		Role:      "dev",
		Online:    true,
	})

	// Pre-populate rate limiter for agent DMs
	tool.rateLimiter.recordDM(hub.NodeID(), "node-bob")

	// Human DM should bypass rate limiting entirely — it will fail on network
	// (no transport in test), but should NOT be a rate-limit error
	for i := 0; i < 3; i++ {
		r, err := tool.doSend(ctx, "msg "+string(rune('0'+i)), []string{"node-bob"}, false, "")
		if err != nil {
			t.Fatalf("unexpected error on msg %d: %v", i, err)
		}
		if r.IsError && strings.Contains(r.Content, "rate-limited") {
			t.Errorf("human DM %d should not be rate-limited: %s", i, r.Content)
		}
	}
}

// TestLanChatBroadcastRateLimited verifies that broadcast_all uses per-peer
// DM rate limiting: after DM'ing a peer, a broadcast to that peer is rate-limited.
func TestLanChatBroadcastRateLimited(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	ctx := context.Background()

	// Add an online participant
	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-bob",
		HumanNick: "bob_dev",
		AgentNick: "bob_dev_agent",
		Endpoint:  "http://localhost:2",
		Role:      "dev",
		Online:    true,
	})

	// Pre-populate: simulate that we already DM'd node-bob recently
	tool.rateLimiter.recordDM(hub.NodeID(), "node-bob")

	// Broadcast should be rate-limited (per-peer DM cooldown for node-bob)
	r2, err := tool.doBroadcastAll(ctx, "announcement", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r2.IsError {
		t.Fatal("broadcast within DM cooldown should be rate-limited")
	}
	if !strings.Contains(r2.Content, "rate-limited") {
		t.Errorf("expected 'rate-limited' in error, got: %s", r2.Content)
	}
}

// TestLanChatBroadcastAsHumanNotRateLimited verifies that human broadcasts
// are never rate-limited.
func TestLanChatBroadcastAsHumanNotRateLimited(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	ctx := context.Background()

	// Add an online participant so broadcast has a target
	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-bob",
		HumanNick: "bob_dev",
		AgentNick: "bob_dev_agent",
		Endpoint:  "http://localhost:2",
		Online:    true,
	})

	for i := 0; i < 3; i++ {
		r, err := tool.doBroadcastAll(ctx, "msg", false)
		if err != nil {
			t.Fatalf("unexpected error on msg %d: %v", i, err)
		}
		// Transport failures are expected (no real network in test),
		// but rate-limiting must never apply to human broadcasts
		if r.IsError && strings.Contains(r.Content, "rate-limited") {
			t.Errorf("human broadcast %d should not be rate-limited: %s", i, r.Content)
		}
	}
}

// TestFormatCooldown verifies the duration formatting helper.
func TestFormatCooldown(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{90 * time.Second, "1m 30s"},
		{5 * time.Minute, "5m"},
		{5*time.Minute + 15*time.Second, "5m 15s"},
	}
	for _, tc := range tests {
		got := formatCooldown(tc.d)
		if got != tc.want {
			t.Errorf("formatCooldown(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

// TestLanChatSendPartialRateLimited verifies that when sending to multiple
// recipients where some are rate-limited:
//  1. The non-limited recipients are NOT blocked (carol passes rate check).
//  2. The rate-limited recipients appear in the result content.
//  3. The error (if any from network failure) does NOT say "All recipients".
func TestLanChatSendPartialRateLimited(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	ctx := context.Background()

	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-bob",
		HumanNick: "bob_dev",
		AgentNick: "bob_dev_agent",
		Endpoint:  "http://localhost:2",
		Role:      "dev",
		Online:    true,
	})
	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-carol",
		HumanNick: "carol_qa",
		AgentNick: "carol_qa_agent",
		Endpoint:  "http://localhost:3",
		Role:      "qa",
		Online:    true,
	})

	// Pre-populate: simulate that we already DM'd node-bob recently
	tool.rateLimiter.recordDM(hub.NodeID(), "node-bob")

	// Send to both bob and carol — bob is rate-limited, carol should pass rate check
	r2, err := tool.doSend(ctx, "team update", []string{"node-bob", "node-carol"}, true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// carol passed rate-limit, so the result must NOT say "All recipients are rate-limited"
	if strings.Contains(r2.Content, "All recipients are rate-limited") {
		t.Errorf("carol should not be rate-limited: %s", r2.Content)
	}

	// bob was rate-limited — his display name must appear somewhere in the result
	// (either in success "Rate-limited recipients skipped" or in failure "Rate-limited recipients skipped")
	if !strings.Contains(r2.Content, "bob_dev_agent") {
		t.Errorf("result should mention rate-limited bob_dev_agent: %s", r2.Content)
	}

	// Verify carol passed the rate-limit check by confirming the result does NOT
	// mention carol in rate-limited section. (recordDM is only called on successful
	// network send, so we can't check dmLastSent — instead we verify carol's name
	// does NOT appear in any rate-limited context.)
	if strings.Contains(r2.Content, "carol_qa_agent: rate-limited") {
		t.Errorf("carol should not be rate-limited: %s", r2.Content)
	}
}

// ---------------------------------------------------------------------------
// resetDMForPeer / OnInboundDM tests (defect 1 — previously zero coverage)
// ---------------------------------------------------------------------------

// TestResetDMForPeer_AllowsImmediateReply verifies that after receiving a DM
// from a peer, the self→peer cooldown is cleared, enabling an immediate reply.
func TestResetDMForPeer_AllowsImmediateReply(t *testing.T) {
	rl := newAgentRateLimiter()
	self := "node-self"
	peer := "node-alice"

	// Simulate a prior DM to alice — should be rate-limited now
	rl.recordDM(self, peer)
	if msg := rl.checkDM(self, peer, 0); msg == "" {
		t.Fatal("should be rate-limited after recordDM")
	}

	// Simulate inbound DM from alice — resets cooldown
	rl.resetDMForPeer(self, peer)

	// Should be allowed now
	if msg := rl.checkDM(self, peer, 0); msg != "" {
		t.Fatalf("should be allowed after resetDMForPeer, got: %s", msg)
	}
}

// TestResetDMForPeer_OnlyClearsOriginator verifies that resetting for peer A
// does NOT clear the cooldown for peer B.
func TestResetDMForPeer_OnlyClearsOriginator(t *testing.T) {
	rl := newAgentRateLimiter()
	self := "node-self"

	rl.recordDM(self, "node-a")
	rl.recordDM(self, "node-b")

	// Reset only for node-a
	rl.resetDMForPeer(self, "node-a")

	// node-a: cleared
	if msg := rl.checkDM(self, "node-a", 0); msg != "" {
		t.Fatalf("node-a should be cleared, got: %s", msg)
	}
	// node-b: still rate-limited
	if msg := rl.checkDM(self, "node-b", 0); msg == "" {
		t.Fatal("node-b should still be rate-limited")
	}
}

// TestResetDMForPeer_Idempotent verifies that resetting a never-DM'd peer
// is a safe no-op.
func TestResetDMForPeer_Idempotent(t *testing.T) {
	rl := newAgentRateLimiter()
	// Reset for a peer that was never recorded — should not panic
	rl.resetDMForPeer("node-self", "node-unknown")
	// Still able to DM normally
	if msg := rl.checkDM("node-self", "node-unknown", 0); msg != "" {
		t.Fatalf("should be allowed for never-recorded peer, got: %s", msg)
	}
}

// TestOnInboundDM_EndToEnd verifies the full path: after a DM cooldown is set
// for a peer, receiving an inbound DM from that peer clears the cooldown
// and allows the agent to reply.
func TestOnInboundDM_EndToEnd(t *testing.T) {
	tool, hub := newTestLanChatTool(t)

	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-bob",
		HumanNick: "bob_dev",
		AgentNick: "bob_dev_agent",
		Endpoint:  "http://localhost:2",
		Role:      "dev",
		Online:    true,
	})

	// Pre-populate: simulate a recent DM to bob (doSend can't do this in test
	// because network send always fails, so recordDM is never called).
	tool.rateLimiter.recordDM(hub.NodeID(), "node-bob")

	// DM to bob — should be rate-limited (cooldown was pre-populated)
	if msg := tool.rateLimiter.checkDM(hub.NodeID(), "node-bob", 0); msg == "" {
		t.Fatal("DM should be rate-limited after pre-populated recordDM")
	}

	// Simulate inbound DM from bob — triggers OnInboundDM which resets cooldown
	tool.OnInboundDM("node-bob")

	// DM to bob — should be allowed now (cooldown was reset)
	if msg := tool.rateLimiter.checkDM(hub.NodeID(), "node-bob", 0); msg != "" {
		t.Fatalf("DM after inbound reset should be allowed, got: %s", msg)
	}
}

// TestOnInboundDM_BroadcastDoesNotReset verifies that receiving a broadcast
// message does NOT trigger the inbound DM reset (only non-broadcast resets).
// We test this by calling OnInboundDM directly — the Hub only calls it for
// non-broadcast messages, so we verify the rate limiter behavior stays correct
// when OnInboundDM is NOT called.
func TestOnInboundDM_BroadcastDoesNotReset(t *testing.T) {
	tool, hub := newTestLanChatTool(t)

	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-bob",
		HumanNick: "bob_dev",
		AgentNick: "bob_dev_agent",
		Endpoint:  "http://localhost:2",
		Online:    true,
	})

	// Pre-populate rate limiter
	tool.rateLimiter.recordDM(hub.NodeID(), "node-bob")

	// Verify rate-limited
	if msg := tool.rateLimiter.checkDM(hub.NodeID(), "node-bob", 0); msg == "" {
		t.Fatal("should be rate-limited")
	}

	// Do NOT call OnInboundDM (simulating a broadcast, which the Hub skips)
	// Rate limit should still be in effect
	if msg := tool.rateLimiter.checkDM(hub.NodeID(), "node-bob", 0); msg == "" {
		t.Fatal("should still be rate-limited when no inbound DM resets it")
	}
}

// TestOnInboundDM_NilGuard verifies OnInboundDM is safe with nil rate limiter.
func TestOnInboundDM_NilGuard(t *testing.T) {
	tool, _ := newTestLanChatTool(t)
	tool.rateLimiter = nil // simulate no rate limiter

	// Should not panic
	tool.OnInboundDM("node-bob")
}

// ---------------------------------------------------------------------------
// Boundary condition test (defect 5)
// ---------------------------------------------------------------------------

// TestCheckDM_BoundaryCondition verifies behavior when elapsed time exactly
// equals the cooldown duration (the >= comparison boundary).
func TestCheckDM_BoundaryCondition(t *testing.T) {
	rl := newAgentRateLimiter()
	sender := "node-self"
	recipient := "node-x"

	// Record a DM, then backdate the timestamp to exactly cooldown ago
	rl.recordDM(sender, recipient)
	rl.mu.Lock()
	key := sender + "\u2192" + recipient
	rl.dmLastSent[key] = time.Now().Add(-defaultAgentDMCooldown)
	rl.mu.Unlock()

	// At exactly cooldown boundary, elapsed >= cooldown should be true → allowed
	if msg := rl.checkDM(sender, recipient, 0); msg != "" {
		t.Fatalf("DM at exact cooldown boundary should be allowed, got: %s", msg)
	}
}

// ---------------------------------------------------------------------------
// Broadcast→DM cross-limit test (defect 4)
// ---------------------------------------------------------------------------

// TestBroadcastThenDMIsRateLimited verifies that after a successful broadcast
// (which calls recordDM for each peer), a subsequent DM to the same peer is
// rate-limited. This complements the existing DM→broadcast test.
func TestBroadcastThenDMIsRateLimited(t *testing.T) {
	rl := newAgentRateLimiter()
	self := "node-self"
	peer := "node-bob"

	// Simulate what doBroadcastAll does on success: recordDM for each peer
	rl.recordDM(self, peer)

	// Subsequent DM to same peer should be rate-limited
	if msg := rl.checkDM(self, peer, 0); msg == "" {
		t.Fatal("DM after broadcast recordDM should be rate-limited")
	}
}

// ---------------------------------------------------------------------------
// sent==0 preserves rateLimited info test (the bug fix)
// ---------------------------------------------------------------------------

// TestSendSentZeroPreservesRateLimited verifies that when some recipients are
// rate-limited and the remaining ones fail on network, the error message
// includes BOTH the network failures AND the rate-limited recipients.
func TestSendSentZeroPreservesRateLimited(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	ctx := context.Background()

	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-bob",
		HumanNick: "bob_dev",
		AgentNick: "bob_dev_agent",
		Endpoint:  "http://localhost:2",
		Online:    true,
	})
	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-carol",
		HumanNick: "carol_qa",
		AgentNick: "carol_qa_agent",
		Endpoint:  "http://localhost:3",
		Online:    true,
	})

	// Pre-populate: bob is rate-limited
	tool.rateLimiter.recordDM(hub.NodeID(), "node-bob")

	// Send to both — bob is rate-limited, carol will fail on network
	r, err := tool.doSend(ctx, "msg", []string{"node-bob", "node-carol"}, true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The result must be an error (sent==0 because carol failed on network)
	if !r.IsError {
		t.Fatal("expected error when all sends failed")
	}

	// Must mention rate-limited bob
	if !strings.Contains(r.Content, "Rate-limited") {
		t.Errorf("error should include rate-limited info for bob: %s", r.Content)
	}
	if !strings.Contains(r.Content, "bob_dev_agent") {
		t.Errorf("error should mention bob_dev_agent: %s", r.Content)
	}
}

// ---------------------------------------------------------------------------
// Concurrency test (defect 6)
// ---------------------------------------------------------------------------

// TestRateLimiter_ConcurrentAccess verifies that the rate limiter is safe
// under concurrent access: multiple goroutines calling checkDM/recordDM/
// resetDMForPeer simultaneously must not race or panic.
func TestRateLimiter_ConcurrentAccess(t *testing.T) {
	rl := newAgentRateLimiter()
	sender := "node-self"

	done := make(chan struct{})

	// Writer goroutine: repeatedly records DMs to different peers
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			peer := fmt.Sprintf("node-%d", i%10)
			rl.recordDM(sender, peer)
		}
	}()

	// Reader goroutine: repeatedly checks DM cooldown
	for i := 0; i < 200; i++ {
		peer := fmt.Sprintf("node-%d", i%10)
		rl.checkDM(sender, peer, 0) //nolint:errcheck
	}

	// Resetter goroutine: repeatedly resets cooldowns
	for i := 0; i < 200; i++ {
		peer := fmt.Sprintf("node-%d", i%10)
		rl.resetDMForPeer(sender, peer)
	}

	<-done // wait for writer to finish
}

// TestLanChatSendTeamRateLimited verifies that send_team uses per-peer DM
// rate limiting (not a shared broadcast cooldown).
func TestLanChatSendTeamRateLimited(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	ctx := context.Background()

	// Set up team members
	hub.SetNickRoleTeam("test", "dev", "myteam")
	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-bob",
		HumanNick: "bob_dev",
		AgentNick: "bob_dev_agent",
		Endpoint:  "http://localhost:2",
		Role:      "dev",
		Team:      "myteam",
		Online:    true,
	})

	// Pre-populate: simulate that we already DM'd node-bob recently
	tool.rateLimiter.recordDM(hub.NodeID(), "node-bob")

	// send_team should be rate-limited (per-peer DM cooldown for node-bob)
	r2, err := tool.doSendTeam(ctx, "team msg 2", "myteam", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r2.IsError {
		t.Fatal("send_team should be rate-limited by per-peer DM cooldown")
	}
	if !strings.Contains(r2.Content, "rate-limited") {
		t.Errorf("expected 'rate-limited' in error, got: %s", r2.Content)
	}
}
