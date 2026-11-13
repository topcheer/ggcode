package tool

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/lanchat"
)

// #4785 (S-14/S-25): the first blocked DM must carry the precise countdown
// and a bounded retry instruction. Pre-fix behavior gave no retry semantics
// and forced blind sleeps of unvalidated length.

var issue4785SecondsRE = regexp.MustCompile(`use sleep tool with seconds=(\d+)`)

func issue4785RetrySeconds(t *testing.T, msg string) int {
	t.Helper()
	m := issue4785SecondsRE.FindStringSubmatch(msg)
	if m == nil {
		t.Fatalf("rate-limit message lacks bounded retry instruction: %q", msg)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("retry seconds not numeric: %v", err)
	}
	return n
}

func TestIssue4785FirstBlockCarriesCountdownAndRetry(t *testing.T) {
	rl := newAgentRateLimiter()
	sender, peer := "node-self", "node-bob"

	if msg := rl.checkDM(sender, peer, 0); msg != "" {
		t.Fatalf("first DM must be allowed, got: %s", msg)
	}
	rl.recordDM(sender, peer)

	msg := rl.checkDM(sender, peer, 0)
	if msg == "" {
		t.Fatal("immediate second DM must be blocked on the first attempt")
	}
	if !strings.Contains(msg, "rate-limited (") || !strings.Contains(msg, "remaining") {
		t.Fatalf("first block must carry the precise countdown, got: %s", msg)
	}
	secs := issue4785RetrySeconds(t, msg)
	if secs < 1 || secs > int(defaultAgentDMCooldown.Seconds())+1 {
		t.Fatalf("retry instruction must be bounded by the cooldown, got seconds=%d (cooldown %s)", secs, defaultAgentDMCooldown)
	}
}

func TestIssue4785CountdownProgressesAcrossChecks(t *testing.T) {
	rl := newAgentRateLimiter()
	sender, peer := "node-self", "node-bob"

	rl.recordDM(sender, peer)
	first := issue4785RetrySeconds(t, rl.checkDM(sender, peer, 0))
	if first < 1 {
		t.Fatalf("first countdown must be positive, got %d", first)
	}

	rl.mu.Lock()
	rl.dmLastSent[sender+"\u2192"+peer] = time.Now().Add(-60 * time.Second)
	rl.mu.Unlock()

	second := issue4785RetrySeconds(t, rl.checkDM(sender, peer, 0))
	if second >= first {
		t.Fatalf("countdown must strictly decrease as cooldown progresses: first=%d second=%d", first, second)
	}

	rl.mu.Lock()
	rl.dmLastSent[sender+"\u2192"+peer] = time.Now().Add(-(defaultAgentDMCooldown + time.Second))
	rl.mu.Unlock()

	if msg := rl.checkDM(sender, peer, 0); msg != "" {
		t.Fatalf("cooldown expiry must allow the DM, got: %s", msg)
	}
}

func TestIssue4785DoSendSurfacesRetryInstructionOnFirstBlock(t *testing.T) {
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

	r, err := tool.doSend(ctx, "urgent update", []string{"node-bob"}, true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.IsError {
		t.Fatal("blocked DM must return an error result")
	}
	if !strings.Contains(r.Content, "rate-limited") {
		t.Fatalf("first blocked send must say rate-limited, got: %s", r.Content)
	}
	issue4785RetrySeconds(t, r.Content)
}
