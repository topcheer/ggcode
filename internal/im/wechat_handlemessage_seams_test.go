package im

import (
	"context"
	"testing"
)

// r208 pin tests for the WechatAdapter.handleMessage inbound seam split.
// Golden filter discipline: these tests were written and verified GREEN on
// the UNREFACTORED handleMessage (base ee693b502) before any extraction;
// after the behavior-preserving split they must pass unchanged.
// Seam-level unit tests for the extracted helpers live in the second half
// of this file (added together with the seams in the r208 commit).

// --- behavior pins (pre-refactor, zero prod-code assumptions) ---

func TestPinHandleMessageDedupGateBeforeRouting(t *testing.T) {
	mgr, bridge := newWechatTestManager(t, "u1")
	adapter, sent := newWechatTestAdapter(t, mgr, nil)

	msg := wechatInboundMsg("u1", "hello", "tok-1", 7)
	adapter.handleMessage(context.Background(), msg)
	adapter.handleMessage(context.Background(), msg) // #973 redelivery

	if n := len(*sent); n != 0 {
		t.Fatalf("dedup skip must not trigger outbound sends, got %d", n)
	}
	if got := bridge.count(); got != 1 {
		t.Fatalf("seen-dedup must drop the redelivered copy before routing, routed=%d want=1", got)
	}
	if last := bridge.last(); last.Envelope.ChannelID != "u1" || last.Text != "hello" {
		t.Fatalf("unexpected routed inbound: %+v", last)
	}
}

func TestPinHandleMessageEmptyTextSilentSkip(t *testing.T) {
	mgr, bridge := newWechatTestManager(t, "u1")
	adapter, sent := newWechatTestAdapter(t, mgr, nil)

	// No items => empty text, no unsupported kind => silent skip (#1251).
	adapter.handleMessage(context.Background(), ilinkMessage{
		MessageID:    8,
		FromUserID:   "u1",
		ToUserID:     "bot",
		MessageType:  ilinkMsgTypeUser,
		ContextToken: "tok-2",
	})

	if n := len(*sent); n != 0 {
		t.Fatalf("empty non-unsupported message must be silently skipped, sends=%d", n)
	}
	if got := bridge.count(); got != 0 {
		t.Fatalf("empty non-unsupported message must not route, routed=%d", got)
	}
}

func TestPinHandleMessageGroupChannelResolution(t *testing.T) {
	mgr, bridge := newWechatTestManager(t, "g9")
	adapter, sent := newWechatTestAdapter(t, mgr, nil)

	msg := wechatInboundMsg("u1", "hi group", "tok-3", 9)
	msg.GroupID = "g9"
	adapter.handleMessage(context.Background(), msg)

	if got := bridge.count(); got != 1 {
		t.Fatalf("authorized group text must route exactly once, routed=%d", got)
	}
	if last := bridge.last(); last.Envelope.ChannelID != "g9" {
		t.Fatalf("group message must resolve ChannelID to the group, got %q", last.Envelope.ChannelID)
	}
	if n := len(*sent); n != 0 {
		t.Fatalf("authorized group text must not trigger notice replies, sends=%d", n)
	}
}

func TestPinHandleMessageUnauthorizedDenied(t *testing.T) {
	mgr, bridge := newWechatTestManager(t, "u1")
	adapter, sent := newWechatTestAdapter(t, mgr, nil)

	// Sender not in the bound channel: routing must deny, exactly one
	// unauthorized/pairing reply goes out, nothing reaches the bridge.
	adapter.handleMessage(context.Background(), wechatInboundMsg("stranger", "knock", "tok-s", 10))

	if got := bridge.count(); got != 0 {
		t.Fatalf("denied inbound must not route, routed=%d", got)
	}
	if n := len(*sent); n != 1 {
		t.Fatalf("denied inbound must produce exactly one reply, sends=%d", n)
	}
}

// --- seam-level unit tests (added together with the r208 seam extraction) ---

func TestSeamWechatUnsupportedNotice(t *testing.T) {
	cases := map[int]string{
		ilinkItemVoice: "[暂不支持语音消息，请发送文字]",
		ilinkItemVideo: "[暂不支持视频消息，请发送文字]",
		ilinkItemImage: "[暂不支持图片消息，请发送文字]",
		ilinkItemFile:  "[暂不支持文件消息，请发送文字]",
		0:              "[暂不支持语音消息，请发送文字]",
		999:            "[暂不支持语音消息，请发送文字]",
	}
	for kind, want := range cases {
		if got := wechatUnsupportedNotice(kind); got != want {
			t.Errorf("wechatUnsupportedNotice(%d) = %q, want %q", kind, got, want)
		}
	}
}

func TestSeamWechatChannelID(t *testing.T) {
	if got := wechatChannelID(ilinkMessage{FromUserID: "u1"}); got != "u1" {
		t.Errorf("direct message: got %q, want u1", got)
	}
	if got := wechatChannelID(ilinkMessage{FromUserID: "u1", GroupID: "g9"}); got != "g9" {
		t.Errorf("group message: got %q, want g9", got)
	}
}

func TestSeamInboundEnvelope(t *testing.T) {
	a := &WechatAdapter{}
	msg := ilinkMessage{MessageID: 12345, FromUserID: "u1", GroupID: "g1"}
	in := a.inboundEnvelope("hello", "g1", msg)
	if in.Envelope.Platform != PlatformWechat {
		t.Fatalf("unexpected platform: %+v", in.Envelope)
	}
	if in.Envelope.ChannelID != "g1" || in.Envelope.SenderID != "u1" {
		t.Fatalf("unexpected channel/sender: %+v", in.Envelope)
	}
	if in.Envelope.MessageID != "12345" {
		t.Fatalf("MessageID must be a decimal string, got %q", in.Envelope.MessageID)
	}
	if in.Text != "hello" || in.Metadata["group_id"] != "g1" {
		t.Fatalf("unexpected text/metadata: %q %+v", in.Text, in.Metadata)
	}
}
