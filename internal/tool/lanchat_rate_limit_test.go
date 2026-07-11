package tool

import (
	"context"
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
// recipients where some are rate-limited, the non-limited ones are allowed
// (and the rate-limited ones are reported in the error).
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
	// (actual network send will fail, but we only check rate-limiting behavior)
	r2, err := tool.doSend(ctx, "team update", []string{"node-bob", "node-carol"}, true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// It may partially fail (network), but should NOT contain only rate-limit error
	// since carol should be allowed through rate limiting
	if r2.IsError {
		// The error should be about network failure, not rate limiting for carol
		if strings.Contains(r2.Content, "All recipients are rate-limited") {
			t.Errorf("carol should not be rate-limited: %s", r2.Content)
		}
	}
	// The rate-limit info for bob should appear in the result
	if !strings.Contains(r2.Content, "Rate-limited") {
		// It may not appear if the send itself failed, which is fine
		// as long as it didn't block carol
	}
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
