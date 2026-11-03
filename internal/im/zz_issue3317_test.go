package im

import (
	"context"
	"strings"
	"testing"
)

func TestIssue3317_EchoSlotRecordedAndNoCollision(t *testing.T) {
	adapter, sent := newQQSendTestAdapter(t)
	mgr := NewManager()
	stored := &ChannelBinding{
		Workspace:            "ws",
		Adapter:              "hermes",
		ChannelID:            "group-1",
		LastInboundMessageID: "msg-42",
		PassiveReplyCount:    1,
	}
	mgr.currentBindings["hermes"] = stored
	adapter.manager = mgr

	if err := adapter.sendReplyText(context.Background(), "group-1", "msg-42", "已在新工作区应答"); err != nil {
		t.Fatalf("echo send failed: %v", err)
	}
	if stored.PassiveReplyCount != 2 {
		t.Fatalf("echo must record its consumed slot: count=%d, want 2 (pre-fix the slot leaked and the next Send collided)", stored.PassiveReplyCount)
	}

	binding := *stored
	if err := adapter.Send(context.Background(), binding, OutboundEvent{Kind: OutboundEventText, Text: "正式回复"}); err != nil {
		t.Fatalf("regular send failed: %v", err)
	}
	for _, r := range *sent {
		if r.Body["msg_seq"] == nil {
			continue
		}
		if f, ok := r.Body["msg_seq"].(float64); ok && int(f) == 2 && r.Body["msg_type"] != nil {
			t.Fatalf("regular Send reused slot 2 burned by the echo (server would dedup it): %s", r.RawBody)
		}
	}
}

func TestIssue3317_PerSendRecordingExactNoDoubleCount(t *testing.T) {
	adapter, sent := newQQSendTestAdapter(t)
	mgr := NewManager()
	stored := &ChannelBinding{
		Workspace:            "ws",
		Adapter:              "hermes",
		ChannelID:            "group-1",
		LastInboundMessageID: "msg-42",
		PassiveReplyCount:    0,
	}
	mgr.currentBindings["hermes"] = stored
	adapter.manager = mgr

	limit := PlatformLimits[PlatformQQ]
	content := strings.Repeat("chunk ", limit*2)
	if err := adapter.Send(context.Background(), *stored, OutboundEvent{Kind: OutboundEventText, Text: content}); err != nil {
		t.Fatalf("send failed: %v", err)
	}

	messages := 0
	for _, r := range *sent {
		if _, ok := r.Body["msg_type"]; ok {
			messages++
		}
	}
	if messages < 2 {
		t.Fatalf("expected >=2 chunked messages, got %d", messages)
	}

	if stored.PassiveReplyCount != messages {
		t.Fatalf("PassiveReplyCount=%d must equal sent messages=%d exactly (no double count)", stored.PassiveReplyCount, messages)
	}
}
