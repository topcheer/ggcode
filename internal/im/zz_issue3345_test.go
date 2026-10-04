package im

// #3345 probes: the typing notify burns a server-side (msg_id, seq) slot
// and must record it like every other passive carrier in qq_adapter.go
// (#3317 discipline chain, fourth member).

import (
	"context"
	"testing"
)

func TestIssue3345_TypingRecordsConsumedSlot(t *testing.T) {
	adapter, sent := newQQSendTestAdapter(t)
	adapter.chatTypes["user-9"] = "c2c"
	mgr := NewManager()
	stored := &ChannelBinding{
		Workspace:            "ws",
		Adapter:              "hermes",
		ChannelID:            "user-9",
		LastInboundMessageID: "msg-42",
		PassiveReplyCount:    1,
	}
	mgr.currentBindings["hermes"] = stored
	adapter.manager = mgr

	if err := adapter.TriggerTyping(context.Background(), *stored); err != nil {
		t.Fatalf("TriggerTyping failed: %v", err)
	}

	// The typing notify carried msg_seq = count+1 = 2.
	typed := false
	for _, r := range *sent {
		if r.Body["msg_type"] == nil {
			continue
		}
		if f, ok := r.Body["msg_type"].(float64); ok && int(f) == 6 {
			typed = true
			if s, _ := r.Body["msg_seq"].(float64); int(s) != 2 {
				t.Fatalf("typing must carry seq=2 (count+1), got %v", r.Body["msg_seq"])
			}
		}
	}
	if !typed {
		t.Fatal("no typing notify (msg_type=6) request captured")
	}
	// #3345: the consumed slot must be recorded, or the next Send reuses
	// seq 2 and the server deduplicates it away.
	if stored.PassiveReplyCount != 2 {
		t.Fatalf("typing must record its consumed slot: count=%d, want 2", stored.PassiveReplyCount)
	}

	// The follow-up regular Send must start PAST the typing's slot.
	if err := adapter.Send(context.Background(), *stored, OutboundEvent{Kind: OutboundEventText, Text: "reply"}); err != nil {
		t.Fatalf("regular send failed: %v", err)
	}
	for _, r := range *sent {
		f, ok := r.Body["msg_seq"].(float64)
		if !ok || r.Body["msg_type"] == nil {
			continue
		}
		if mt, _ := r.Body["msg_type"].(float64); int(mt) == 6 {
			continue // the typing itself
		}
		if int(f) == 2 {
			t.Fatalf("regular Send reused slot 2 burned by the typing notify (server would dedup it): %s", r.RawBody)
		}
	}
}
