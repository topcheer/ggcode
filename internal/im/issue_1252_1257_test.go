package im

// Regression tests for GitHub issues #1252-#1257 (wecom + whatsapp).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func wecomInboundPayload(msgType string, bodyExtras map[string]any) map[string]any {
	body := map[string]any{
		"msgid":    "m-1",
		"chatid":   "chat-1",
		"chattype": "single",
		"msgtype":  msgType,
		"from":     map[string]any{"userid": "user-1"},
	}
	for k, v := range bodyExtras {
		body[k] = v
	}
	return map[string]any{
		"headers": map[string]any{"req_id": "req-1"},
		"body":    body,
	}
}

func newWecomInboundHarness(t *testing.T) (*wecomAdapter, *stubInboundBridge, *Manager) {
	t.Helper()
	mgr := NewManager()
	bridge := &stubInboundBridge{}
	mgr.SetBridge(bridge)
	mgr.SetBindingStore(NewMemoryBindingStore())
	mgr.BindSession(SessionBinding{Workspace: "/ws"})
	mgr.currentBindings["wc"] = &ChannelBinding{
		Workspace: "/ws",
		Platform:  PlatformWeCom,
		Adapter:   "wc",
		ChannelID: "chat-1",
	}
	a := &wecomAdapter{
		name:        "wc",
		manager:     mgr,
		seen:        map[string]time.Time{},
		replyReqIDs: map[string]string{},
	}
	return a, bridge, mgr
}

// TestWecomPureImageMessageRoutedWithAttachment pins #1252: a pure media
// message (image, no text) used to hit `text == "" → return` BEFORE
// extractAttachments and was dropped entirely; it must now route with its
// attachment.
func TestWecomPureImageMessageRoutedWithAttachment(t *testing.T) {
	a, bridge, _ := newWecomInboundHarness(t)

	a.handleMessage(context.Background(), wecomInboundPayload("image", map[string]any{
		"image": map[string]any{"url": "https://example.com/shot.png"},
	}))

	msg := bridge.last()
	if msg.Text != "" {
		t.Fatalf("pure image message should carry no text, got %q", msg.Text)
	}
	if len(msg.Attachments) != 1 {
		t.Fatalf("#1252: pure image message dropped its attachment (got %d); must route with 1", len(msg.Attachments))
	}
	if msg.Attachments[0].Kind != AttachmentImage || msg.Attachments[0].URL != "https://example.com/shot.png" {
		t.Fatalf("unexpected attachment: %+v", msg.Attachments[0])
	}
}

// TestWecomPureFileMessageRouted pins #1252 for the file variant.
func TestWecomPureFileMessageRouted(t *testing.T) {
	a, bridge, _ := newWecomInboundHarness(t)

	a.handleMessage(context.Background(), wecomInboundPayload("file", map[string]any{
		"file": map[string]any{"url": "https://example.com/doc.pdf"},
	}))

	if len(bridge.last().Attachments) != 1 {
		t.Fatalf("#1252: pure file message must route with its attachment")
	}
}

// TestWecomMixedMessageImageCollected pins #1253: mixed messages carry
// per-item msgtypes; the image item of a "text + screenshot" composition
// used to vanish (text arrived, image in neither text nor attachments).
func TestWecomMixedMessageImageCollected(t *testing.T) {
	a, bridge, _ := newWecomInboundHarness(t)

	a.handleMessage(context.Background(), wecomInboundPayload("mixed", map[string]any{
		"mixed": map[string]any{
			"msg_item": []any{
				map[string]any{
					"msgtype": "text",
					"text":    map[string]any{"content": "这是报错截图"},
				},
				map[string]any{
					"msgtype": "image",
					"image":   map[string]any{"url": "https://example.com/mixed-shot.png"},
				},
			},
		},
	}))

	msg := bridge.last()
	if !strings.Contains(msg.Text, "这是报错截图") {
		t.Fatalf("mixed text missing: %q", msg.Text)
	}
	if len(msg.Attachments) != 1 || msg.Attachments[0].URL != "https://example.com/mixed-shot.png" {
		t.Fatalf("#1253: mixed image item lost, attachments = %+v", msg.Attachments)
	}
}

// TestWecomPairingFlowNoWarningState guards the #1255 downgrade: the normal
// pairing flow (unbound channel, instructions consumed) must not publish any
// warning state, and the reply path stays intact.
func TestWecomPairingFlowNoWarningState(t *testing.T) {
	mgr := NewManager()
	mgr.SetBindingStore(NewMemoryBindingStore())
	a := &wecomAdapter{
		name:        "wc",
		manager:     mgr,
		seen:        map[string]time.Time{},
		replyReqIDs: map[string]string{},
	}

	a.handleMessage(context.Background(), wecomInboundPayload("text", map[string]any{
		"text": map[string]any{"content": "hello"},
	}))

	mgr.mu.RLock()
	state, ok := mgr.adapters["wc"]
	mgr.mu.RUnlock()
	if ok && state.Status == "warning" {
		t.Fatalf("#1255: pairing flow must not pin a warning state: %+v", state)
	}
}

// TestWecomUploadChunkAckRetried pins #1254's retry half: a lost chunk ack
// must be retried (chunk upload is idempotent) instead of aborting the whole
// upload.
func TestWecomUploadChunkAckRetried(t *testing.T) {
	f := newFakeWeComServer(t)
	defer f.srv.Close()
	f.chunkDrops = 1
	a := newWecomMediaAdapter(t, f)
	a.ackTimeout = 300 * time.Millisecond // keep the dropped-ack wait short

	data := make([]byte, 600<<10) // 2 chunks
	mediaID, err := a.wecomUploadMedia(context.Background(), data, "shot.png")
	if err != nil {
		t.Fatalf("#1254: chunk ack loss must be retried, got: %v", err)
	}
	if mediaID != "MEDIA123" {
		t.Fatalf("media_id = %q", mediaID)
	}
	// 2 logical chunks + 1 re-sent chunk = 3 chunk frames on the wire.
	if got := f.cmdCount(wecomCmdUploadChunk); got != 3 {
		t.Fatalf("expected 3 chunk frames (2 chunks + 1 retry), got %d", got)
	}
}

// TestWecomMultiImageSendSpacing pins #1254's spacing half: two images plus
// a text chunk must be spaced by wecomInterMsgDelay after every image and
// before the first proactive text (3 × 600ms total).
func TestWecomMultiImageSendSpacing(t *testing.T) {
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(make([]byte, 4<<10))
	}))
	defer imgSrv.Close()

	f := newFakeWeComServer(t)
	defer f.srv.Close()
	a := newWecomMediaAdapter(t, f)

	start := time.Now()
	err := a.Send(context.Background(), ChannelBinding{
		ChannelID: "chat-1",
		Platform:  PlatformWeCom,
		Adapter:   "wc",
		Workspace: "/ws",
		TargetID:  "chat-1",
	}, OutboundEvent{Kind: OutboundEventText, Text: "![a](" + imgSrv.URL + "/1.png) ![b](" + imgSrv.URL + "/2.png) see these two shots"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	elapsed := time.Since(start)
	if min := 3 * wecomInterMsgDelay; elapsed < min-150*time.Millisecond {
		t.Fatalf("#1254: 2 images + first text completed in %v; expected >= ~%v of spacing", elapsed, min)
	}
}

// TestWhatsappMediaCaption pins #1257: media messages carry their
// accompanying text in Caption fields; without extraction, "screenshot +
// question" messages were dropped entirely.
func TestWhatsappMediaCaption(t *testing.T) {
	cases := []struct {
		name string
		msg  *waE2E.Message
		want string
	}{
		{"image caption", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("这个报错怎么修")}}, "这个报错怎么修"},
		{"video caption", &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Caption: proto.String("看这段录像")}}, "看这段录像"},
		{"document caption", &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{Caption: proto.String("见附件说明")}}, "见附件说明"},
		{"no media", &waE2E.Message{Conversation: proto.String("plain")}, ""},
		{"nil message", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := whatsappMediaCaption(tc.msg); got != tc.want {
				t.Fatalf("whatsappMediaCaption = %q, want %q", got, tc.want)
			}
		})
	}
}
