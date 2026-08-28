package im

// Regression tests for GitHub issues #1240-#1243 (tg_adapter):
//
//	#1240: inbound photo/document caption merged into text
//	#1241: legacy HTML parse mode escapes & < >
//	#1242: sendPhotoByUpload 429 retry + image-loop inter-message delay
//	#1243: message-level errors no longer publish a warning state

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingBridge1240 struct {
	mu    sync.Mutex
	count int
	last  InboundMessage
}

func (r *recordingBridge1240) SubmitInboundMessage(_ context.Context, msg InboundMessage) error {
	r.mu.Lock()
	r.count++
	r.last = msg
	r.mu.Unlock()
	return nil
}

func newTGAdapterFor1240(t *testing.T) (*tgAdapter, *recordingBridge1240, *Manager) {
	t.Helper()
	bridge := &recordingBridge1240{}
	m := NewManager()
	m.bridge = bridge
	m.bindingStore = NewMemoryBindingStore()
	m.BindSession(SessionBinding{Workspace: "/tmp/ws-1240", SessionID: "sess-1"})
	m.currentBindings["tg"] = &ChannelBinding{Workspace: "/tmp/ws-1240", Adapter: "tg", ChannelID: "100"}
	a := &tgAdapter{
		name:       "tg",
		manager:    m,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		seen:       make(map[int]time.Time),
	}
	return a, bridge, m
}

// TestTGInboundCaptionMergedIntoText pins #1240: a photo message carries its
// accompanying instruction in caption (no text field) — the caption must
// reach the agent instead of being silently dropped.
func TestTGInboundCaptionMergedIntoText(t *testing.T) {
	a, bridge, _ := newTGAdapterFor1240(t)

	a.handleUpdate(context.Background(), map[string]any{
		"update_id": 200,
		"message": map[string]any{
			"message_id": 10.0,
			"date":       float64(time.Now().Unix()),
			"chat":       map[string]any{"id": 100.0, "type": "private"},
			"from":       map[string]any{"id": 7.0, "first_name": "Ada"},
			"caption":    "看这个报错截图，是 panic 了",
			"photo": []any{
				map[string]any{"file_id": "f1", "width": 100.0, "height": 100.0},
				map[string]any{"file_id": "f2", "width": 400.0, "height": 400.0},
			},
		},
	})

	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if bridge.count != 1 {
		t.Fatalf("expected 1 inbound message, got %d", bridge.count)
	}
	if !strings.Contains(bridge.last.Text, "panic") {
		t.Fatalf("#1240: caption must be merged into text, got %q", bridge.last.Text)
	}
	// (Attachment download requires a live Bot API; the caption merge is
	// the behavior under test here.)
}

// TestTGInboundGroupCaptionMentionStripped pins #1240's group variant: the
// caption goes through the same structured mention stripping as text, using
// caption_entities.
func TestTGInboundGroupCaptionMentionStripped(t *testing.T) {
	a, bridge, _ := newTGAdapterFor1240(t)
	a.mu.Lock()
	a.botUsername = "mybot"
	a.mu.Unlock()

	a.handleUpdate(context.Background(), map[string]any{
		"update_id": 201,
		"message": map[string]any{
			"message_id": 11.0,
			"date":       float64(time.Now().Unix()),
			"chat":       map[string]any{"id": 100.0, "type": "supergroup"},
			"from":       map[string]any{"id": 7.0, "first_name": "Ada"},
			"caption":    "@mybot check this log",
			"photo": []any{
				map[string]any{"file_id": "f3", "width": 10.0, "height": 10.0},
			},
		},
	})

	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if bridge.count != 1 {
		t.Fatalf("expected 1 inbound message, got %d", bridge.count)
	}
	if got := bridge.last.Text; got != "check this log" {
		t.Fatalf("group caption must have the bot mention stripped, got %q", got)
	}
}

// TestTGFormatMessagesHTMLEscape pins #1241: legacy HTML mode must escape
// & < > so code snippets and comparisons don't 400 the whole message.
func TestTGFormatMessagesHTMLEscape(t *testing.T) {
	a := newTGTestAdapter("", "HTML")
	msgs, err := a.formatMessages("i < len(s) & a > b")
	if err != nil {
		t.Fatalf("formatMessages: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("no messages")
	}
	want := "i &lt; len(s) &amp; a &gt; b"
	if msgs[0].Text != want {
		t.Fatalf("HTML mode must escape &<>: got %q, want %q", msgs[0].Text, want)
	}
	if got := a.legacyCaption("a<b&c"); got != "a&lt;b&amp;c" {
		t.Fatalf("legacyCaption HTML escaping: got %q", got)
	}
}

// TestTGSendPhotoUploadRetries429 pins #1242: the multipart upload path must
// honor Telegram's 429 retry_after instead of failing hard (the caller's
// image loop drops the photo silently).
func TestTGSendPhotoUploadRetries429(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/botTESTTOKEN/sendPhoto") {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		requests++
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":1}}`)
			return
		}
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":5}}`)
	}))
	defer srv.Close()

	a := newTGTestAdapter(srv.URL, "")
	start := time.Now()
	if err := a.sendPhotoByUpload(context.Background(), "100", []byte("fakepng"), "img.png", "", ""); err != nil {
		t.Fatalf("sendPhotoByUpload must survive a 429: %v", err)
	}
	if requests != 2 {
		t.Fatalf("expected exactly 2 upload requests (429 + success), got %d", requests)
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Fatalf("retry must honor retry_after=1s, completed in %v", elapsed)
	}
}

// TestTGSendImageLoopDelay pins #1242: every delivered photo is followed by
// the inter-message delay, so a 2-image reply cannot burst past ~1 msg/s
// (and the photo->text transition is spaced too).
func TestTGSendImageLoopDelay(t *testing.T) {
	var sends int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":7}}`)
		_ = sends
	}))
	defer srv.Close()

	a := newTGTestAdapter(srv.URL, "")
	a.manager = nil // not needed for outbound-only

	img1 := tinyPNGBase64(t, 255, 0, 0)
	img2 := tinyPNGBase64(t, 0, 255, 0)
	content := "![a](data:image/png;base64," + img1 + ") ![b](data:image/png;base64," + img2 + ") trailing text"
	start := time.Now()
	if err := a.Send(context.Background(), ChannelBinding{ChannelID: "100"}, OutboundEvent{Kind: OutboundEventText, Text: content}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	elapsed := time.Since(start)
	// 2 delivered images = 2 inter-message gaps (img1->img2, img2->text).
	if elapsed < 1500*time.Millisecond {
		t.Fatalf("2-image send completed in %v; expected >= ~2s of inter-message spacing (#1242)", elapsed)
	}
}

// TestTGMessageLevelErrorNoWarningState pins #1243: an inbound processing
// error (here: manager without session) must not publish a warning adapter
// state that nothing would ever recover from.
func TestTGMessageLevelErrorNoWarningState(t *testing.T) {
	a, _, m := newTGAdapterFor1240(t)
	// Drop the session so HandleInbound fails with a non-ErrNoChannelBound
	// error — previously this published state {Healthy:false, Status:"warning"}
	// forever (connected is only published once, before the poll loop).
	m.mu.Lock()
	m.session = nil
	m.mu.Unlock()

	a.handleUpdate(context.Background(), map[string]any{
		"update_id": 202,
		"message": map[string]any{
			"message_id": 12.0,
			"date":       float64(time.Now().Unix()),
			"chat":       map[string]any{"id": 100.0, "type": "private"},
			"from":       map[string]any{"id": 7.0, "first_name": "Ada"},
			"text":       "hello",
		},
	})

	m.mu.Lock()
	state, exists := m.adapters["tg"]
	m.mu.Unlock()
	if exists && state.Status == "warning" {
		t.Fatalf("#1243: message-level error must not publish a sticky warning state, got %+v", state)
	}
}
