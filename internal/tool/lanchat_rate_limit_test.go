package tool

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/lanchat"
)

func TestAgentRateLimiter_DMCooldownPerRecipient(t *testing.T) {
	rl := newAgentRateLimiter()
	sender := "node-self"

	if msg := rl.checkDM(sender, "node-a", 0); msg != "" {
		t.Fatalf("first DM to node-a should be allowed, got: %s", msg)
	}
	rl.recordDM(sender, "node-a")

	msg := rl.checkDM(sender, "node-a", 0)
	if msg == "" {
		t.Fatal("second DM to node-a within cooldown should be rate-limited")
	}
	if !strings.Contains(msg, "rate-limited") {
		t.Errorf("error should mention rate-limited, got: %s", msg)
	}

	if msg := rl.checkDM(sender, "node-b", 0); msg != "" {
		t.Fatalf("first DM to node-b should be allowed, got: %s", msg)
	}
	rl.recordDM(sender, "node-b")

	rl.mu.Lock()
	key := sender + "\u2192" + "node-a"
	rl.dmLastSent[key] = time.Now().Add(-(defaultAgentDMCooldown + time.Second))
	rl.mu.Unlock()

	if msg := rl.checkDM(sender, "node-a", 0); msg != "" {
		t.Fatalf("DM to node-a after cooldown should be allowed, got: %s", msg)
	}
}

func TestAgentRateLimiter_DifferentSenders(t *testing.T) {
	rl := newAgentRateLimiter()

	if msg := rl.checkDM("sender-a", "node-c", 0); msg != "" {
		t.Fatalf("sender-a → node-c should be allowed, got: %s", msg)
	}
	rl.recordDM("sender-a", "node-c")

	if msg := rl.checkDM("sender-b", "node-c", 0); msg != "" {
		t.Fatalf("sender-b → node-c should be allowed (different sender), got: %s", msg)
	}

	if msg := rl.checkDM("sender-a", "node-c", 0); msg == "" {
		t.Fatal("sender-a → node-c should be rate-limited")
	}
}

func TestAgentRateLimiter_CustomCooldown(t *testing.T) {
	rl := newAgentRateLimiter()
	sender := "node-self"

	if msg := rl.checkDM(sender, "node-x", 0); msg != "" {
		t.Fatalf("first DM should be allowed, got: %s", msg)
	}
	rl.recordDM(sender, "node-x")

	if msg := rl.checkDM(sender, "node-x", 10*time.Second); msg == "" {
		t.Fatal("second DM within 10s custom cooldown should be rate-limited")
	}

	rl.mu.Lock()
	key := sender + "\u2192" + "node-x"
	rl.dmLastSent[key] = time.Now().Add(-11 * time.Second)
	rl.mu.Unlock()

	if msg := rl.checkDM(sender, "node-x", 10*time.Second); msg != "" {
		t.Fatalf("DM after custom cooldown expiry should be allowed, got: %s", msg)
	}
}

func TestLanChatSendRateLimited(t *testing.T) {
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

	tool.rateLimiter.recordDM(hub.NodeID(), "node-bob")

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

	if r3.IsError && strings.Contains(r3.Content, "rate-limited") {
		t.Errorf("DM to different recipient should not be rate-limited: %s", r3.Content)
	}
}

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

	tool.rateLimiter.recordDM(hub.NodeID(), "node-bob")

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

func TestLanChatBroadcastRateLimited(t *testing.T) {
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

	tool.rateLimiter.recordDM(hub.NodeID(), "node-bob")

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

func TestLanChatBroadcastAsHumanNotRateLimited(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	ctx := context.Background()

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

		if r.IsError && strings.Contains(r.Content, "rate-limited") {
			t.Errorf("human broadcast %d should not be rate-limited: %s", i, r.Content)
		}
	}
}

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

	tool.rateLimiter.recordDM(hub.NodeID(), "node-bob")

	r2, err := tool.doSend(ctx, "team update", []string{"node-bob", "node-carol"}, true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(r2.Content, "All recipients are rate-limited") {
		t.Errorf("carol should not be rate-limited: %s", r2.Content)
	}

	if !strings.Contains(r2.Content, "bob_dev_agent") {
		t.Errorf("result should mention rate-limited bob_dev_agent: %s", r2.Content)
	}

	if strings.Contains(r2.Content, "carol_qa_agent: rate-limited") {
		t.Errorf("carol should not be rate-limited: %s", r2.Content)
	}
}

func TestResetDMForPeer_AllowsImmediateReply(t *testing.T) {
	rl := newAgentRateLimiter()
	self := "node-self"
	peer := "node-alice"

	rl.recordDM(self, peer)
	if msg := rl.checkDM(self, peer, 0); msg == "" {
		t.Fatal("should be rate-limited after recordDM")
	}

	rl.resetDMForPeer(self, peer)

	if msg := rl.checkDM(self, peer, 0); msg != "" {
		t.Fatalf("should be allowed after resetDMForPeer, got: %s", msg)
	}
}

func TestResetDMForPeer_OnlyClearsOriginator(t *testing.T) {
	rl := newAgentRateLimiter()
	self := "node-self"

	rl.recordDM(self, "node-a")
	rl.recordDM(self, "node-b")

	rl.resetDMForPeer(self, "node-a")

	if msg := rl.checkDM(self, "node-a", 0); msg != "" {
		t.Fatalf("node-a should be cleared, got: %s", msg)
	}

	if msg := rl.checkDM(self, "node-b", 0); msg == "" {
		t.Fatal("node-b should still be rate-limited")
	}
}

func TestResetDMForPeer_Idempotent(t *testing.T) {
	rl := newAgentRateLimiter()

	rl.resetDMForPeer("node-self", "node-unknown")

	if msg := rl.checkDM("node-self", "node-unknown", 0); msg != "" {
		t.Fatalf("should be allowed for never-recorded peer, got: %s", msg)
	}
}

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

	tool.rateLimiter.recordDM(hub.NodeID(), "node-bob")

	if msg := tool.rateLimiter.checkDM(hub.NodeID(), "node-bob", 0); msg == "" {
		t.Fatal("DM should be rate-limited after pre-populated recordDM")
	}

	tool.OnInboundDM("node-bob")

	if msg := tool.rateLimiter.checkDM(hub.NodeID(), "node-bob", 0); msg != "" {
		t.Fatalf("DM after inbound reset should be allowed, got: %s", msg)
	}
}

func TestOnInboundDM_BroadcastDoesNotReset(t *testing.T) {
	tool, hub := newTestLanChatTool(t)

	hub.HandlePresence(lanchat.Participant{
		NodeID:    "node-bob",
		HumanNick: "bob_dev",
		AgentNick: "bob_dev_agent",
		Endpoint:  "http://localhost:2",
		Online:    true,
	})

	tool.rateLimiter.recordDM(hub.NodeID(), "node-bob")

	if msg := tool.rateLimiter.checkDM(hub.NodeID(), "node-bob", 0); msg == "" {
		t.Fatal("should be rate-limited")
	}

	if msg := tool.rateLimiter.checkDM(hub.NodeID(), "node-bob", 0); msg == "" {
		t.Fatal("should still be rate-limited when no inbound DM resets it")
	}
}

func TestOnInboundDM_NilGuard(t *testing.T) {
	tool, _ := newTestLanChatTool(t)
	tool.rateLimiter = nil

	tool.OnInboundDM("node-bob")
}

func TestCheckDM_BoundaryCondition(t *testing.T) {
	rl := newAgentRateLimiter()
	sender := "node-self"
	recipient := "node-x"

	rl.recordDM(sender, recipient)
	rl.mu.Lock()
	key := sender + "\u2192" + recipient
	rl.dmLastSent[key] = time.Now().Add(-defaultAgentDMCooldown)
	rl.mu.Unlock()

	if msg := rl.checkDM(sender, recipient, 0); msg != "" {
		t.Fatalf("DM at exact cooldown boundary should be allowed, got: %s", msg)
	}
}

func TestBroadcastThenDMIsRateLimited(t *testing.T) {
	rl := newAgentRateLimiter()
	self := "node-self"
	peer := "node-bob"

	rl.recordDM(self, peer)

	if msg := rl.checkDM(self, peer, 0); msg == "" {
		t.Fatal("DM after broadcast recordDM should be rate-limited")
	}
}

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

	tool.rateLimiter.recordDM(hub.NodeID(), "node-bob")

	r, err := tool.doSend(ctx, "msg", []string{"node-bob", "node-carol"}, true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !r.IsError {
		t.Fatal("expected error when all sends failed")
	}

	if !strings.Contains(r.Content, "Rate-limited") {
		t.Errorf("error should include rate-limited info for bob: %s", r.Content)
	}
	if !strings.Contains(r.Content, "bob_dev_agent") {
		t.Errorf("error should mention bob_dev_agent: %s", r.Content)
	}
}

func TestRateLimiter_ConcurrentAccess(t *testing.T) {
	rl := newAgentRateLimiter()
	sender := "node-self"

	done := make(chan struct{})

	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			peer := fmt.Sprintf("node-%d", i%10)
			rl.recordDM(sender, peer)
		}
	}()

	for i := 0; i < 200; i++ {
		peer := fmt.Sprintf("node-%d", i%10)
		rl.checkDM(sender, peer, 0) //nolint:errcheck
	}

	for i := 0; i < 200; i++ {
		peer := fmt.Sprintf("node-%d", i%10)
		rl.resetDMForPeer(sender, peer)
	}

	<-done
}

func TestLanChatSendTeamRateLimited(t *testing.T) {
	tool, hub := newTestLanChatTool(t)
	ctx := context.Background()

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

	tool.rateLimiter.recordDM(hub.NodeID(), "node-bob")

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
