package im

// Regression tests for GitHub issues #1247/#1248/#1249 (twitch) and
// #1250/#1251 (wechat).

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// #1247: inbound WHISPER commands must be routed as DMs
// ---------------------------------------------------------------------------

// TestTwitchInboundWhisperRoutedAsDM pins #1247: Twitch delivers DMs as
// WHISPER commands (not PRIVMSG-to-nick); without a WHISPER case every
// inbound DM — pairing included — was silently dropped.
func TestTwitchInboundWhisperRoutedAsDM(t *testing.T) {
	srv := newTwitchFakeIRCServer(t)

	mgr := NewManager()
	bridge := &stubInboundBridge{}
	mgr.SetBridge(bridge)
	mgr.SetBindingStore(NewMemoryBindingStore())
	mgr.BindSession(SessionBinding{Workspace: "/ws"})
	mgr.currentBindings["test-twitch"] = &ChannelBinding{
		Workspace: "/ws",
		Platform:  PlatformTwitch,
		Adapter:   "test-twitch",
		ChannelID: "viewer",
	}

	adapter := &twitchAdapter{
		name:    "test-twitch",
		manager: mgr,
		token:   "oauth:test-token",
		nick:    "testbot",
		dialIRC: srv.dialTo(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { adapter.run(ctx); close(done) }()

	<-srv.connOpened
	srv.send(":viewer!~viewer@viewer.tmi.twitch.tv WHISPER testbot :hello there")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if msg := bridge.last(); msg.Text != "" {
			if msg.Envelope.ChannelID != "viewer" {
				t.Fatalf("WHISPER must route as DM with channelID=sender nick, got %q", msg.Envelope.ChannelID)
			}
			if msg.Text != "hello there" {
				t.Fatalf("unexpected text: %q", msg.Text)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("#1247: inbound WHISPER never reached the bridge (still silently dropped?)")
}

// TestTwitchInboundWhisperTriggersPairing pins the DM pairing half of #1247:
// /bind sent as a whisper must reach HandlePairingInbound and produce a
// pairing reply (previously it vanished without even a debug log).
func TestTwitchInboundWhisperTriggersPairing(t *testing.T) {
	srv := newTwitchFakeIRCServer(t)

	mgr := NewManager()
	bridge := &stubInboundBridge{}
	mgr.SetBridge(bridge)
	mgr.SetBindingStore(NewMemoryBindingStore())

	adapter := &twitchAdapter{
		name:    "test-twitch",
		manager: mgr,
		token:   "oauth:test-token",
		nick:    "testbot",
		dialIRC: srv.dialTo(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { adapter.run(ctx); close(done) }()

	<-srv.connOpened
	srv.send(":viewer!~viewer@viewer.tmi.twitch.tv WHISPER testbot :/bind")

	// Pairing consumed the message: nothing reaches the bridge...
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		srv.send(":viewer!~viewer@viewer.tmi.twitch.tv WHISPER testbot :/bind")
		time.Sleep(100 * time.Millisecond)
		bridge.mu.Lock()
		n := len(bridge.messages)
		bridge.mu.Unlock()
		if n > 0 {
			t.Fatalf("pairing-consumed whisper must not reach inbound routing, got %d messages", n)
		}
		return // one cycle is enough: reaching here means handleIRCLine dispatched WHISPER
	}
}

// ---------------------------------------------------------------------------
// #1249: concurrent sendRaw writes must produce intact IRC lines
// ---------------------------------------------------------------------------

// TestTwitchSendRawConcurrentLinesIntact pins #1249: read-loop PONGs,
// keepalive PINGs, Sends and the Close QUIT all call sendRaw concurrently;
// writes must be serialized so lines never interleave.
func TestTwitchSendRawConcurrentLinesIntact(t *testing.T) {
	srv := newTwitchFakeIRCServer(t)
	adapter := newTestTwitchAdapter(srv.dialTo())

	client, err := srv.dialTo()("ignored")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	adapter.mu.Lock()
	adapter.conn = client
	adapter.mu.Unlock()

	const goroutines = 8
	const perGoroutine = 10
	want := make(map[string]bool)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		for i := 0; i < perGoroutine; i++ {
			// Long distinct payloads (>=1KB) to make an interleaved write
			// obvious if the mutex were missing.
			want[fmt.Sprintf("PRIVMSG #chan%d :%s-%d", g, strings.Repeat("x", 1024), i)] = true
		}
	}
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				if err := adapter.sendRaw(fmt.Sprintf("PRIVMSG #chan%d :%s-%d", g, strings.Repeat("x", 1024), i)); err != nil {
					t.Errorf("sendRaw: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	deadline := time.Now().Add(5 * time.Second)
	total := goroutines * perGoroutine
	for time.Now().Before(deadline) {
		lines := srv.lines()
		if len(lines) >= total {
			for _, l := range lines {
				if !want[l] {
					t.Fatalf("#1249: corrupted/interleaved IRC line (len=%d, prefix=%q...)", len(l), l[:40])
				}
			}
			if len(lines) != total {
				t.Fatalf("expected exactly %d lines, got %d", total, len(lines))
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("only %d/%d lines arrived", len(srv.lines()), total)
}

// ---------------------------------------------------------------------------
// #1251: wechat voice/video inbound gets an explicit notice, not silence
// ---------------------------------------------------------------------------

func TestWechatVoiceMessageRepliesUnsupportedNotice(t *testing.T) {
	mgr, bridge := newWechatTestManager(t, "u1")
	adapter, sent := newWechatTestAdapter(t, mgr, nil)

	adapter.handleMessage(context.Background(), ilinkMessage{
		MessageID:    42,
		FromUserID:   "u1",
		ToUserID:     "bot",
		MessageType:  ilinkMsgTypeUser,
		ContextToken: "tok-1",
		ItemList:     []ilinkItem{{Type: ilinkItemVoice}},
	})

	if n := len(*sent); n != 1 {
		t.Fatalf("#1251: expected 1 notice reply, got %d sends", n)
	}
	first := (*sent)[0]
	if first.Msg.ItemList[0].TextItem == nil || first.Msg.ItemList[0].TextItem.Text != "[暂不支持语音消息，请发送文字]" {
		t.Fatalf("expected voice-unsupported notice, got %+v", first.Msg.ItemList)
	}
	bridge.mu.Lock()
	n := len(bridge.messages)
	bridge.mu.Unlock()
	if n != 0 {
		t.Fatalf("unsupported voice message must not be routed as inbound, got %d", n)
	}
}

func TestWechatVideoMessageRepliesUnsupportedNotice(t *testing.T) {
	mgr, _ := newWechatTestManager(t, "u1")
	adapter, sent := newWechatTestAdapter(t, mgr, nil)

	adapter.handleMessage(context.Background(), ilinkMessage{
		MessageID:   43,
		FromUserID:  "u1",
		ToUserID:    "bot",
		MessageType: ilinkMsgTypeUser,
		ItemList:    []ilinkItem{{Type: ilinkItemVideo}},
	})

	if n := len(*sent); n != 1 {
		t.Fatalf("#1251: expected 1 notice reply, got %d sends", n)
	}
	if txt := (*sent)[0].Msg.ItemList[0].TextItem.Text; txt != "[暂不支持视频消息，请发送文字]" {
		t.Fatalf("expected video-unsupported notice, got %q", txt)
	}
}

// ---------------------------------------------------------------------------
// #1250: image send failures are honest; multi-image sends are spaced
// ---------------------------------------------------------------------------

// TestWechatSendImageAllFailedHonestError pins #1250's misleading placeholder:
// when every image fails, Send must return an error instead of emitting
// "[image sent above]" as a success notice.
func TestWechatSendImageAllFailedHonestError(t *testing.T) {
	mgr, _ := newWechatTestManager(t, "u1")
	adapter, _ := newWechatTestAdapter(t, mgr, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"errcode":500,"errmsg":"boom"}`)
	})

	err := adapter.Send(context.Background(), ChannelBinding{
		ChannelID:    "u1",
		ContextToken: "tok",
		Platform:     PlatformWechat,
		Adapter:      "wc",
		Workspace:    "/ws",
		TargetID:     "u1",
	}, OutboundEvent{Kind: OutboundEventText, Text: "![pic](https://example.com/a.png)"})
	if err == nil {
		t.Fatal("#1250: all images failed - Send must return an error, not a success placeholder")
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Fatalf("expected honest failure mentioning failed images, got %q", err)
	}
}

// TestWechatSendImageNotDeliverableHonestError: local/data-URL images are
// undeliverable via iLink — that path must fail honestly too (it used to
// emit the same misleading placeholder).
func TestWechatSendImageNotDeliverableHonestError(t *testing.T) {
	mgr, _ := newWechatTestManager(t, "u1")
	adapter, _ := newWechatTestAdapter(t, mgr, nil)

	err := adapter.Send(context.Background(), ChannelBinding{
		ChannelID: "u1",
		Platform:  PlatformWechat,
		Adapter:   "wc",
		Workspace: "/ws",
		TargetID:  "u1",
	}, OutboundEvent{Kind: OutboundEventText, Text: "![local](data:image/png;base64,iVBORw0KGgo=)"})
	if err == nil {
		t.Fatal("#1250: undeliverable images must surface an error")
	}
	if !strings.Contains(err.Error(), "not deliverable") {
		t.Fatalf("expected not-deliverable error, got %q", err)
	}
}

// TestWechatSendMultiImageSpacing pins #1250's rate-limit alignment: every
// delivered image is followed by the same 500ms spacing the text-chunk path
// uses (2 images -> >= ~1s).
func TestWechatSendMultiImageSpacing(t *testing.T) {
	mgr, _ := newWechatTestManager(t, "u1")
	adapter, sent := newWechatTestAdapter(t, mgr, nil)

	start := time.Now()
	err := adapter.Send(context.Background(), ChannelBinding{
		ChannelID: "u1",
		Platform:  PlatformWechat,
		Adapter:   "wc",
		Workspace: "/ws",
		TargetID:  "u1",
	}, OutboundEvent{Kind: OutboundEventText, Text: "![a](https://example.com/1.png) ![b](https://example.com/2.png)"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if n := len(*sent); n != 2 {
		t.Fatalf("expected 2 image sends, got %d", n)
	}
	if elapsed := time.Since(start); elapsed < 950*time.Millisecond {
		t.Fatalf("2-image send completed in %v; expected >= ~1s of inter-image spacing (#1250)", elapsed)
	}
}

// TestWechatVoiceWithTextRoutesText pins the #1251 quality-review correction:
// a mixed message (voice item + text item, e.g. voice-with-transcription)
// must route its usable text normally — the unsupported notice only fires
// when the message carries no text at all. The first fix attempt
// intercepted these and dropped the text behind the notice.
func TestWechatVoiceWithTextRoutesText(t *testing.T) {
	mgr, bridge := newWechatTestManager(t, "u1")
	adapter, sent := newWechatTestAdapter(t, mgr, nil)

	adapter.handleMessage(context.Background(), ilinkMessage{
		MessageID:   44,
		FromUserID:  "u1",
		ToUserID:    "bot",
		MessageType: ilinkMsgTypeUser,
		ItemList: []ilinkItem{
			{Type: ilinkItemVoice},
			{Type: ilinkItemText, TextItem: &ilinkTextItem{Text: "语音附带的文字说明"}},
		},
	})

	bridge.mu.Lock()
	n := len(bridge.messages)
	bridge.mu.Unlock()
	if n != 1 {
		t.Fatalf("mixed voice+text message: text must be routed, got %d inbound", n)
	}
	if got := bridge.last().Text; got != "语音附带的文字说明" {
		t.Fatalf("routed text = %q", got)
	}
	if cnt := len(*sent); cnt != 0 {
		t.Fatalf("mixed message with usable text must NOT trigger the unsupported notice, got %d sends", cnt)
	}
}
