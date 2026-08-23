package im

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// --- test helpers ---

// stubInboundBridge captures inbound messages submitted by HandleInbound.
type stubInboundBridge struct {
	mu        sync.Mutex
	messages  []InboundMessage
	submitErr error
}

func (b *stubInboundBridge) SubmitInboundMessage(ctx context.Context, msg InboundMessage) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.submitErr != nil {
		return b.submitErr
	}
	b.messages = append(b.messages, msg)
	return nil
}

func (b *stubInboundBridge) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.messages)
}

func (b *stubInboundBridge) last() InboundMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.messages) == 0 {
		return InboundMessage{}
	}
	return b.messages[len(b.messages)-1]
}

// newWechatTestManager builds a manager with a bound wechat adapter channel and
// a recording bridge, ready for handleMessage tests.
func newWechatTestManager(t *testing.T, channelID string) (*Manager, *stubInboundBridge) {
	t.Helper()
	mgr := NewManager()
	bridge := &stubInboundBridge{}
	mgr.SetBridge(bridge)
	mgr.SetBindingStore(NewMemoryBindingStore())
	mgr.BindSession(SessionBinding{Workspace: "/ws"})
	mgr.currentBindings["wc"] = &ChannelBinding{
		Workspace: "/ws",
		Platform:  PlatformWechat,
		Adapter:   "wc",
		ChannelID: channelID,
	}
	return mgr, bridge
}

// newWechatTestAdapter builds an adapter whose sends go to a recording server.
func newWechatTestAdapter(t *testing.T, mgr *Manager, handler http.HandlerFunc) (*WechatAdapter, *[]ilinkSendMessageRequest) {
	t.Helper()
	var mu sync.Mutex
	sent := []ilinkSendMessageRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handler != nil {
			handler(w, r)
			return
		}
		var body ilinkSendMessageRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		sent = append(sent, body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ret":0}`)
	}))
	t.Cleanup(srv.Close)
	a, err := newWechatAdapter("wc", config.IMConfig{}, config.IMAdapterConfig{
		Extra: map[string]any{
			"base_url":  srv.URL,
			"bot_token": "test-token",
		},
	}, mgr)
	if err != nil {
		t.Fatalf("newWechatAdapter: %v", err)
	}
	return a, &sent
}

func wechatInboundMsg(fromUserID, text, contextToken string, messageID int64) ilinkMessage {
	return ilinkMessage{
		MessageID:    messageID,
		FromUserID:   fromUserID,
		ToUserID:     "bot",
		MessageType:  ilinkMsgTypeUser,
		ContextToken: contextToken,
		ItemList:     []ilinkItem{{Type: ilinkItemText, TextItem: &ilinkTextItem{Text: text}}},
	}
}

// --- Issue #973 problem 1: pre-auth token poisoning ---

// An unauthorized user's message must NOT overwrite the authorized binding's
// context_token (previously UpdateBindingContextToken ran before the
// HandleInbound channel check).
func TestWechatHandleMessage_UnauthorizedUserDoesNotOverwriteContextToken(t *testing.T) {
	mgr, bridge := newWechatTestManager(t, "user-authorized")
	a, _ := newWechatTestAdapter(t, mgr, nil)

	// Authorized channel's token in the binding.
	mgr.currentBindings["wc"].ContextToken = "authorized-token"
	mgr.currentBindings["wc"].ContextTokenUpdatedAt = time.Now()

	// Stranger (different channel ID) sends a message with their own token.
	a.handleMessage(context.Background(), wechatInboundMsg("stranger-1", "hello", "stranger-token", 101))

	if got := mgr.GetBindingContextToken("wc"); got != "authorized-token" {
		t.Fatalf("pre-auth poisoning: binding token = %q, want %q (stranger overwrote it)", got, "authorized-token")
	}
	// The unauthorized message must still be rejected by channel auth.
	if bridge.count() != 0 {
		t.Fatalf("unauthorized message reached the bridge: %+v", bridge.last())
	}
}

// An authorized user's message DOES refresh the binding's context_token (the
// fix must not break the legitimate refresh path).
func TestWechatHandleMessage_AuthorizedUserRefreshesContextToken(t *testing.T) {
	mgr, bridge := newWechatTestManager(t, "user-authorized")
	a, _ := newWechatTestAdapter(t, mgr, nil)

	mgr.currentBindings["wc"].ContextToken = "stale-token"

	a.handleMessage(context.Background(), wechatInboundMsg("user-authorized", "hello", "fresh-token", 102))

	if got := mgr.GetBindingContextToken("wc"); got != "fresh-token" {
		t.Fatalf("authorized refresh broken: binding token = %q, want %q", got, "fresh-token")
	}
	if bridge.count() != 1 {
		t.Fatalf("authorized message should reach the bridge once, got %d", bridge.count())
	}
}

// Pairing flow still works after moving the token update: an unbound adapter's
// first inbound gets a pairing challenge reply, and a successful pairing
// persists the pairing user's token.
func TestWechatHandleMessage_PairingFlowStillWorks(t *testing.T) {
	mgr := NewManager()
	bridge := &stubInboundBridge{}
	mgr.SetBridge(bridge)
	mgr.SetBindingStore(NewMemoryBindingStore())
	mgr.BindSession(SessionBinding{Workspace: "/ws"})
	// No binding yet → first message triggers pairing challenge.
	a, sent := newWechatTestAdapter(t, mgr, nil)

	// Step 1: stranger's first message → challenge issued (consumed).
	a.handleMessage(context.Background(), wechatInboundMsg("pairer", "hi", "pairer-token", 201))
	if bridge.count() != 0 {
		t.Fatalf("pairing-consumed message should not reach the bridge, got %d", bridge.count())
	}
	if len(*sent) == 0 {
		t.Fatal("expected a pairing challenge reply to be sent")
	}
	// Challenge phase: not yet bound → token must NOT be persisted.
	if got := mgr.GetBindingContextToken("wc"); got != "" {
		t.Fatalf("challenge phase: binding token = %q, want empty (pre-auth poisoning)", got)
	}
	if mgr.pendingPairing == nil {
		t.Fatal("expected pending pairing challenge")
	}

	// Step 2: correct code → pairing success, token persisted.
	code := mgr.pendingPairing.Code
	(*sent) = nil
	a.handleMessage(context.Background(), wechatInboundMsg("pairer", code, "pairer-token-2", 202))
	if len(*sent) == 0 {
		t.Fatal("expected pairing-success reply")
	}
	if !strings.Contains((*sent)[0].Msg.ItemList[0].TextItem.Text, "绑定成功") {
		t.Fatalf("expected success reply, got %q", (*sent)[0].Msg.ItemList[0].TextItem.Text)
	}
	if got := mgr.GetBindingContextToken("wc"); got != "pairer-token-2" {
		t.Fatalf("pairing success should persist the pairing user's token, got %q", got)
	}
	// The reply must carry the pairing user's own context_token.
	if (*sent)[0].Msg.ContextToken != "pairer-token-2" {
		t.Fatalf("pairing reply token = %q, want pairer-token-2", (*sent)[0].Msg.ContextToken)
	}
}

// The unauthorized-rejection reply must carry the STRANGER's context_token,
// not the authorized binding's token (per-conversation tokens).
func TestWechatHandleMessage_UnauthorizedReplyUsesStrangerToken(t *testing.T) {
	mgr, _ := newWechatTestManager(t, "user-authorized")
	a, sent := newWechatTestAdapter(t, mgr, nil)
	mgr.currentBindings["wc"].ContextToken = "authorized-token"

	a.handleMessage(context.Background(), wechatInboundMsg("stranger-9", "let me in", "stranger-token", 203))

	if len(*sent) == 0 {
		t.Fatal("expected unauthorized-rejection reply")
	}
	if (*sent)[0].Msg.ToUserID != "stranger-9" {
		t.Fatalf("rejection sent to %q, want stranger-9", (*sent)[0].Msg.ToUserID)
	}
	if (*sent)[0].Msg.ContextToken != "stranger-token" {
		t.Fatalf("rejection reply token = %q, want stranger-token (per-conversation tokens)", (*sent)[0].Msg.ContextToken)
	}
	if got := mgr.GetBindingContextToken("wc"); got != "authorized-token" {
		t.Fatalf("binding token must be untouched: got %q", got)
	}
}

// --- Issue #973 problem 2: silent swallowed replies ---

func TestWechatSendTextToUser_EmptyTokenReturnsError(t *testing.T) {
	mgr, _ := newWechatTestManager(t, "user-1")
	a, _ := newWechatTestAdapter(t, mgr, nil)
	a.mu.Lock()
	a.botToken = ""
	a.mu.Unlock()

	err := a.sendTextToUser(context.Background(), "user-1", "hi", "tok")
	if err == nil {
		t.Fatal("expected error for empty bot_token, got nil")
	}
	if !strings.Contains(err.Error(), "no bot_token") {
		t.Errorf("error should mention no bot_token, got: %v", err)
	}
}

func TestWechatSendTextToUser_EmptyContentReturnsError(t *testing.T) {
	mgr, _ := newWechatTestManager(t, "user-1")
	a, _ := newWechatTestAdapter(t, mgr, nil)

	err := a.sendTextToUser(context.Background(), "user-1", "   ", "tok")
	if err == nil {
		t.Fatal("expected error for empty content, got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention empty, got: %v", err)
	}
}

func TestWechatSendTextToUser_Success(t *testing.T) {
	mgr, _ := newWechatTestManager(t, "user-1")
	a, sent := newWechatTestAdapter(t, mgr, nil)

	if err := a.sendTextToUser(context.Background(), "user-1", "hello", "ctx-tok"); err != nil {
		t.Fatalf("sendTextToUser: %v", err)
	}
	if len(*sent) != 1 {
		t.Fatalf("expected 1 send, got %d", len(*sent))
	}
	if (*sent)[0].Msg.ContextToken != "ctx-tok" {
		t.Errorf("ContextToken = %q, want ctx-tok", (*sent)[0].Msg.ContextToken)
	}
}

// --- Issue #973 problem 3: quota enforcement ---

// A stale context_token (older than the documented ~24h TTL) must fail with a
// visible, actionable error instead of a guaranteed server-side rejection.
func TestWechatSend_StaleContextTokenFails(t *testing.T) {
	mgr, _ := newWechatTestManager(t, "user-1")
	a, _ := newWechatTestAdapter(t, mgr, nil)

	err := a.Send(context.Background(), ChannelBinding{
		ChannelID:             "user-1",
		ContextToken:          "stale",
		ContextTokenUpdatedAt: time.Now().Add(-25 * time.Hour),
	}, OutboundEvent{Kind: OutboundEventText, Text: "hi"})
	if err == nil {
		t.Fatal("expected error for stale context_token")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("error should mention expiry, got: %v", err)
	}
}

// A fresh token and a zero timestamp (legacy binding, unknown age) both pass.
func TestWechatSend_FreshOrUnknownAgeTokenPasses(t *testing.T) {
	mgr, _ := newWechatTestManager(t, "user-1")
	a, _ := newWechatTestAdapter(t, mgr, nil)

	for name, updated := range map[string]time.Time{
		"fresh":  time.Now(),
		"legacy": {},
	} {
		err := a.Send(context.Background(), ChannelBinding{
			ChannelID:             "user-1",
			ContextToken:          "tok",
			ContextTokenUpdatedAt: updated,
		}, OutboundEvent{Kind: OutboundEventText, Text: "hi"})
		if err != nil {
			t.Errorf("%s: unexpected error: %v", name, err)
		}
	}
}

// Long output is capped at wechatMaxChunksPerSend chunks, with a visible
// truncation notice on the last chunk.
func TestWechatSend_ChunkCapWithTruncationNotice(t *testing.T) {
	mgr, _ := newWechatTestManager(t, "user-1")
	a, sent := newWechatTestAdapter(t, mgr, nil)

	// Build text long enough for many chunks: repeat a multi-KB block.
	block := strings.Repeat("ggcode 测试内容块。", 200) // ~2.6KB per block
	long := strings.Repeat(block+"\n", 12)        // forces > 5 chunks

	if err := a.Send(context.Background(), ChannelBinding{
		ChannelID:             "user-1",
		ContextToken:          "tok",
		ContextTokenUpdatedAt: time.Now(),
	}, OutboundEvent{Kind: OutboundEventText, Text: long}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if len(*sent) != wechatMaxChunksPerSend {
		t.Fatalf("sent %d chunks, want cap %d", len(*sent), wechatMaxChunksPerSend)
	}
	last := (*sent)[len(*sent)-1].Msg.ItemList[0].TextItem.Text
	if !strings.Contains(last, "[消息过长，已截断]") {
		t.Errorf("last chunk missing truncation notice: %q", tail(last, 60))
	}
	// Every chunk must respect the platform byte limit.
	maxBytes := PlatformLimits[PlatformWechat]
	for i, s := range *sent {
		if n := len(s.Msg.ItemList[0].TextItem.Text); n > maxBytes {
			t.Errorf("chunk %d exceeds byte limit: %d > %d", i, n, maxBytes)
		}
	}
}

// Short output is unaffected by the cap (single chunk, no notice).
func TestWechatSend_ShortOutputUnaffected(t *testing.T) {
	mgr, _ := newWechatTestManager(t, "user-1")
	a, sent := newWechatTestAdapter(t, mgr, nil)

	err := a.Send(context.Background(), ChannelBinding{
		ChannelID:             "user-1",
		ContextToken:          "tok",
		ContextTokenUpdatedAt: time.Now(),
	}, OutboundEvent{Kind: OutboundEventText, Text: "short message"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(*sent) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(*sent))
	}
	if strings.Contains((*sent)[0].Msg.ItemList[0].TextItem.Text, "已截断") {
		t.Error("short message must not carry a truncation notice")
	}
}

// --- Issue #973 attachment: inbound MsgId dedup ---

func TestWechatSeenMessageID_Dedup(t *testing.T) {
	mgr, bridge := newWechatTestManager(t, "user-1")
	a, _ := newWechatTestAdapter(t, mgr, nil)

	msg := wechatInboundMsg("user-1", "hello", "tok", 301)
	a.handleMessage(context.Background(), msg)
	a.handleMessage(context.Background(), msg) // redelivery
	a.handleMessage(context.Background(), msg) // redelivery

	if bridge.count() != 1 {
		t.Fatalf("duplicate redeliveries were not deduped: bridge got %d", bridge.count())
	}
}

func TestWechatSeenMessageID_DifferentIDsPass(t *testing.T) {
	mgr, bridge := newWechatTestManager(t, "user-1")
	a, _ := newWechatTestAdapter(t, mgr, nil)

	a.handleMessage(context.Background(), wechatInboundMsg("user-1", "a", "t", 401))
	a.handleMessage(context.Background(), wechatInboundMsg("user-1", "b", "t", 402))

	if bridge.count() != 2 {
		t.Fatalf("distinct message IDs must both pass, got %d", bridge.count())
	}
}

func TestWechatSeenMessageID_ZeroIDPasses(t *testing.T) {
	a, _ := newWechatAdapter("wc", config.IMConfig{}, config.IMAdapterConfig{}, nil)
	for i := 0; i < 3; i++ {
		if a.seenMessageID(0) {
			t.Fatal("zero ID must never be treated as duplicate")
		}
	}
}

func TestWechatSeenMessageID_CapacityBounded(t *testing.T) {
	a, _ := newWechatAdapter("wc", config.IMConfig{}, config.IMAdapterConfig{}, nil)
	for i := 1; i <= wechatSeenMsgCapacity*3; i++ {
		a.seenMessageID(int64(i))
	}
	a.mu.RLock()
	size := len(a.seen)
	a.mu.RUnlock()
	if size > wechatSeenMsgCapacity {
		t.Fatalf("dedup map grew past capacity: %d > %d", size, wechatSeenMsgCapacity)
	}
}

// Concurrent handleMessage calls must not race (go test -race covers this).
func TestWechatHandleMessage_ConcurrentNoRace(t *testing.T) {
	mgr, bridge := newWechatTestManager(t, "user-1")
	a, _ := newWechatTestAdapter(t, mgr, nil)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			a.handleMessage(context.Background(), wechatInboundMsg("user-1", "msg", "tok", id))
		}(int64(1000 + i))
	}
	wg.Wait()
	if bridge.count() != 16 {
		t.Fatalf("expected 16 inbounds, got %d", bridge.count())
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
