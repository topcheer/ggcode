package im

// Issue #719: pairing challenge hardening tests.
// 1. Challenge TTL expiry (stale slot discarded, new request creates fresh challenge).
// 2. Cross-channel preemption after idle.
// 3. Wrong-code attempt limit (slot cleared at 5 wrong attempts, RejectCount
//    advances the existing blacklist mechanism).

import (
	"strings"
	"testing"
	"time"
)

func newPairingTestManager(t *testing.T) *Manager {
	t.Helper()
	mgr := NewManager()
	if err := mgr.SetBindingStore(NewMemoryBindingStore()); err != nil {
		t.Fatalf("SetBindingStore: %v", err)
	}
	if err := mgr.SetPairingStore(NewMemoryPairingStore()); err != nil {
		t.Fatalf("SetPairingStore: %v", err)
	}
	mgr.BindSession(SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := mgr.BindChannel(ChannelBinding{
		Platform: PlatformQQ,
		Adapter:  "qq",
		TargetID: "ops",
	}); err != nil {
		t.Fatalf("BindChannel: %v", err)
	}
	return mgr
}

func pairingMsg(adapter, channelID, messageID, text string, at time.Time) InboundMessage {
	return InboundMessage{
		Envelope: Envelope{
			Adapter:    adapter,
			Platform:   PlatformQQ,
			ChannelID:  channelID,
			SenderID:   "user-1",
			SenderName: "tester",
			MessageID:  messageID,
			ReceivedAt: at,
		},
		Text: text,
	}
}

// agePendingPairing rewinds the pending challenge's timestamps by d so tests
// can simulate elapsed time without waiting.
func agePendingPairing(mgr *Manager, d time.Duration) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if mgr.pendingPairing == nil {
		return
	}
	mgr.pendingPairing.RequestedAt = mgr.pendingPairing.RequestedAt.Add(-d)
	if !mgr.pendingPairing.LastInboundAt.IsZero() {
		mgr.pendingPairing.LastInboundAt = mgr.pendingPairing.LastInboundAt.Add(-d)
	}
}

func TestPairingChallengeTTLExpiry(t *testing.T) {
	tests := []struct {
		name         string
		age          time.Duration
		otherChan    bool   // request comes from a different channel
		wantStale    bool   // true = stale slot must be dropped, new challenge created
		wantHeld     bool   // true = old slot must be kept ("other channel waiting" reply)
		wantReplyHas string // substring expected in reply
	}{
		{
			name:         "same channel within TTL keeps challenge",
			age:          pairingChallengeTTL - time.Minute,
			wantHeld:     true,
			wantReplyHas: "绑定码不正确",
		},
		{
			name:         "same channel beyond TTL discards and recreates",
			age:          pairingChallengeTTL + time.Minute,
			wantStale:    true,
			wantReplyHas: "4 位绑定码",
		},
		{
			name:         "other channel idle under preempt window holds slot",
			age:          pairingPreemptIdleAfter - time.Minute,
			otherChan:    true,
			wantHeld:     true,
			wantReplyHas: "其他渠道在等待绑定",
		},
		{
			name:         "other channel idle beyond preempt window preempts",
			age:          pairingPreemptIdleAfter + time.Minute,
			otherChan:    true,
			wantStale:    true,
			wantReplyHas: "4 位绑定码",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := newPairingTestManager(t)
			now := time.Now()

			if _, err := mgr.HandlePairingInbound(pairingMsg("qq", "group-1", "msg-1", "start", now)); err != nil {
				t.Fatalf("initial HandlePairingInbound: %v", err)
			}
			original := mgr.Snapshot().PendingPairing
			if original == nil || original.Code == "" {
				t.Fatalf("expected pending challenge after initial request, got %#v", original)
			}

			agePendingPairing(mgr, tt.age)

			channel := "group-1"
			if tt.otherChan {
				channel = "group-2"
			}
			wrong := "0000"
			if wrong == original.Code {
				wrong = "1111"
			}
			second, err := mgr.HandlePairingInbound(pairingMsg("qq", channel, "msg-2", wrong, now.Add(tt.age)))
			if err != nil {
				t.Fatalf("second HandlePairingInbound: %v", err)
			}
			if !second.Consumed {
				t.Fatalf("expected second message consumed, got %#v", second)
			}
			if !strings.Contains(second.ReplyText, tt.wantReplyHas) {
				t.Fatalf("reply %q does not contain %q", second.ReplyText, tt.wantReplyHas)
			}

			after := mgr.Snapshot().PendingPairing
			if tt.wantStale {
				if after == nil || after.Code == "" {
					t.Fatalf("expected fresh challenge after stale slot dropped, got %#v", after)
				}
				if after.ChannelID != channel {
					t.Fatalf("expected fresh challenge for channel %s, got %#v", channel, after)
				}
			}
			if tt.wantHeld {
				if after == nil || after.Code != original.Code {
					t.Fatalf("expected original challenge retained, got %#v want code %s", after, original.Code)
				}
			}
		})
	}
}

func TestPairingWrongCodeAttemptLimitClearsSlot(t *testing.T) {
	mgr := newPairingTestManager(t)
	now := time.Now()

	first, err := mgr.HandlePairingInbound(pairingMsg("qq", "group-1", "msg-start", "start", now))
	if err != nil {
		t.Fatalf("initial HandlePairingInbound: %v", err)
	}
	if !first.Consumed {
		t.Fatalf("expected initial request consumed, got %#v", first)
	}
	challenge := mgr.Snapshot().PendingPairing
	if challenge == nil {
		t.Fatal("expected pending challenge")
	}
	correctCode := challenge.Code

	// Submit wrong codes; each must be rejected but keep the slot, except the
	// final attempt which must clear the slot.
	for i := 1; i <= maxPairingWrongCodeAttempts; i++ {
		wrong := "9999"
		if wrong == correctCode {
			wrong = "8888"
		}
		res, err := mgr.HandlePairingInbound(pairingMsg("qq", "group-1", "msg-wrong", wrong, now.Add(time.Duration(i)*time.Second)))
		if err != nil {
			t.Fatalf("wrong-code attempt %d: %v", i, err)
		}
		if !res.Consumed {
			t.Fatalf("wrong-code attempt %d not consumed: %#v", i, res)
		}
		pending := mgr.Snapshot().PendingPairing
		if i < maxPairingWrongCodeAttempts {
			if pending == nil || pending.WrongCodeAttempts != i {
				t.Fatalf("after attempt %d: expected slot kept with WrongCodeAttempts=%d, got %#v", i, i, pending)
			}
			if !strings.Contains(res.ReplyText, "绑定码不正确") {
				t.Fatalf("attempt %d reply should say wrong code, got %q", i, res.ReplyText)
			}
		} else {
			if pending != nil {
				t.Fatalf("after final attempt: expected slot cleared, got %#v", pending)
			}
			if !strings.Contains(res.ReplyText, "错误次数过多") {
				t.Fatalf("final attempt reply should announce cancellation, got %q", res.ReplyText)
			}
		}
	}

	// A new request from the same channel must be able to create a fresh
	// challenge (slot self-healed).
	fresh, err := mgr.HandlePairingInbound(pairingMsg("qq", "group-1", "msg-fresh", "start", now.Add(2*time.Minute)))
	if err != nil {
		t.Fatalf("fresh HandlePairingInbound: %v", err)
	}
	if !fresh.Consumed || !strings.Contains(fresh.ReplyText, "4 位绑定码") {
		t.Fatalf("expected fresh challenge after slot cleared, got %#v", fresh)
	}
}

func TestPairingWrongCodeExhaustionAdvancesBlacklist(t *testing.T) {
	store := NewMemoryPairingStore()
	mgr := NewManager()
	if err := mgr.SetBindingStore(NewMemoryBindingStore()); err != nil {
		t.Fatalf("SetBindingStore: %v", err)
	}
	if err := mgr.SetPairingStore(store); err != nil {
		t.Fatalf("SetPairingStore: %v", err)
	}
	mgr.BindSession(SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := mgr.BindChannel(ChannelBinding{Platform: PlatformQQ, Adapter: "qq", TargetID: "ops"}); err != nil {
		t.Fatalf("BindChannel: %v", err)
	}
	now := time.Now()

	// Exhaust wrong-code attempts pairingBlacklistAfterRejects times. Each
	// exhaustion advances RejectCount by 1; the third must blacklist.
	for round := 1; round <= pairingBlacklistAfterRejects; round++ {
		if _, err := mgr.HandlePairingInbound(pairingMsg("qq", "group-1", "msg-start", "start", now)); err != nil {
			t.Fatalf("round %d start: %v", round, err)
		}
		for i := 0; i < maxPairingWrongCodeAttempts; i++ {
			if _, err := mgr.HandlePairingInbound(pairingMsg("qq", "group-1", "msg-wrong", "0000", now)); err != nil {
				t.Fatalf("round %d wrong attempt %d: %v", round, i, err)
			}
		}
	}

	// After the third exhaustion the channel must be blacklisted: further
	// pairing requests get the blacklist reply and never create a challenge.
	res, err := mgr.HandlePairingInbound(pairingMsg("qq", "group-1", "msg-after", "start", now))
	if err != nil {
		t.Fatalf("post-blacklist HandlePairingInbound: %v", err)
	}
	if !res.Consumed || !strings.Contains(res.ReplyText, "黑名单") {
		t.Fatalf("expected blacklist reply after %d exhaustions, got %#v", pairingBlacklistAfterRejects, res)
	}
	if pending := mgr.Snapshot().PendingPairing; pending != nil {
		t.Fatalf("expected no pending challenge on blacklisted channel, got %#v", pending)
	}
}
