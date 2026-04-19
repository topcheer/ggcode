package im

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// ============================================================
// Manager.TriggerTyping
// ============================================================

// stubTypingSink implements both Sink and TypingIndicator.
type stubTypingSink struct {
	name        string
	typingCalls []ChannelBinding
	typingErr   error
}

func (s *stubTypingSink) Name() string { return s.name }
func (s *stubTypingSink) Send(_ context.Context, _ ChannelBinding, _ OutboundEvent) error {
	return nil
}
func (s *stubTypingSink) TriggerTyping(_ context.Context, binding ChannelBinding) error {
	s.typingCalls = append(s.typingCalls, binding)
	return s.typingErr
}

// stubNonTypingSink implements Sink but NOT TypingIndicator.
type stubNonTypingSink struct {
	name string
}

func (s *stubNonTypingSink) Name() string { return s.name }
func (s *stubNonTypingSink) Send(_ context.Context, _ ChannelBinding, _ OutboundEvent) error {
	return nil
}

func TestTriggerTyping_SendsToTypingIndicatorSinks(t *testing.T) {
	mgr := NewManager()
	_ = mgr.SetBindingStore(NewMemoryBindingStore())
	mgr.BindSession(SessionBinding{SessionID: "s1", Workspace: "/tmp/project"})
	_, _ = mgr.BindChannel(ChannelBinding{
		Platform:  PlatformQQ,
		Adapter:   "qq",
		ChannelID: "ch-1",
	})

	sink := &stubTypingSink{name: "qq"}
	mgr.RegisterSink(sink)

	mgr.TriggerTyping(context.Background())

	if len(sink.typingCalls) != 1 {
		t.Fatalf("expected 1 TriggerTyping call, got %d", len(sink.typingCalls))
	}
	if sink.typingCalls[0].ChannelID != "ch-1" {
		t.Errorf("binding ChannelID = %q, want ch-1", sink.typingCalls[0].ChannelID)
	}
}

func TestTriggerTyping_SkipsNonTypingIndicatorSinks(t *testing.T) {
	mgr := NewManager()
	_ = mgr.SetBindingStore(NewMemoryBindingStore())
	mgr.BindSession(SessionBinding{SessionID: "s1", Workspace: "/tmp/project"})
	_, _ = mgr.BindChannel(ChannelBinding{
		Platform:  PlatformQQ,
		Adapter:   "qq",
		ChannelID: "ch-1",
	})

	sink := &stubNonTypingSink{name: "qq"}
	mgr.RegisterSink(sink)

	// Should not panic; non-TypingIndicator sinks are silently skipped.
	mgr.TriggerTyping(context.Background())
}

func TestTriggerTyping_SkipsEmptyChannelID(t *testing.T) {
	mgr := NewManager()
	_ = mgr.SetBindingStore(NewMemoryBindingStore())
	mgr.BindSession(SessionBinding{SessionID: "s1", Workspace: "/tmp/project"})
	_, _ = mgr.BindChannel(ChannelBinding{
		Platform: PlatformQQ,
		Adapter:  "qq",
		// ChannelID is empty — no typing indicator should be sent
	})

	sink := &stubTypingSink{name: "qq"}
	mgr.RegisterSink(sink)

	mgr.TriggerTyping(context.Background())

	if len(sink.typingCalls) != 0 {
		t.Errorf("expected 0 TriggerTyping calls for empty channel, got %d", len(sink.typingCalls))
	}
}

func TestTriggerTyping_NoBindings(t *testing.T) {
	mgr := NewManager()
	_ = mgr.SetBindingStore(NewMemoryBindingStore())
	mgr.BindSession(SessionBinding{SessionID: "s1", Workspace: "/tmp/project"})

	// No bindings — should not panic.
	mgr.TriggerTyping(context.Background())
}

func TestTriggerTyping_MultipleAdapters(t *testing.T) {
	mgr := NewManager()
	_ = mgr.SetBindingStore(NewMemoryBindingStore())
	mgr.BindSession(SessionBinding{SessionID: "s1", Workspace: "/tmp/project"})
	_, _ = mgr.BindChannel(ChannelBinding{
		Platform:  PlatformQQ,
		Adapter:   "qq",
		ChannelID: "ch-1",
	})
	_, _ = mgr.BindChannel(ChannelBinding{
		Platform:  PlatformTelegram,
		Adapter:   "tg",
		ChannelID: "ch-2",
	})

	qqSink := &stubTypingSink{name: "qq"}
	tgSink := &stubTypingSink{name: "tg"}
	mgr.RegisterSink(qqSink)
	mgr.RegisterSink(tgSink)

	mgr.TriggerTyping(context.Background())

	if len(qqSink.typingCalls) != 1 {
		t.Errorf("qq: expected 1 call, got %d", len(qqSink.typingCalls))
	}
	if len(tgSink.typingCalls) != 1 {
		t.Errorf("tg: expected 1 call, got %d", len(tgSink.typingCalls))
	}
}

// ============================================================
// Telegram TriggerTyping
// ============================================================

func TestTGTriggerTyping_SendsChatAction(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	adapter := &tgAdapter{
		httpClient: srv.Client(),
		botToken:   "TEST_TOKEN",
		apiBase:    srv.URL,
	}

	err := adapter.TriggerTyping(context.Background(), ChannelBinding{ChannelID: "12345"})
	if err != nil {
		t.Fatalf("TriggerTyping: %v", err)
	}

	if !strings.Contains(gotPath, "/botTEST_TOKEN/sendChatAction") {
		t.Errorf("path = %q, want /botTEST_TOKEN/sendChatAction", gotPath)
	}
	if gotBody["chat_id"] != "12345" {
		t.Errorf("chat_id = %v, want 12345", gotBody["chat_id"])
	}
	if gotBody["action"] != "typing" {
		t.Errorf("action = %v, want typing", gotBody["action"])
	}
}

func TestTGTriggerTyping_EmptyChannel(t *testing.T) {
	adapter := &tgAdapter{}
	err := adapter.TriggerTyping(context.Background(), ChannelBinding{ChannelID: ""})
	if err != nil {
		t.Errorf("expected nil for empty channel, got %v", err)
	}
}

func TestTGTriggerTyping_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	adapter := &tgAdapter{
		httpClient: srv.Client(),
		botToken:   "TOKEN",
		apiBase:    srv.URL,
	}

	err := adapter.TriggerTyping(context.Background(), ChannelBinding{ChannelID: "123"})
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

// ============================================================
// Discord TriggerTyping
// ============================================================

func TestDiscordTriggerTyping_PostsToTypingEndpoint(t *testing.T) {
	var gotPath string
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	adapter := &discordAdapter{
		httpClient: srv.Client(),
		token:      "BOT_TOKEN",
		apiBase:    srv.URL,
	}

	err := adapter.TriggerTyping(context.Background(), ChannelBinding{ChannelID: "999"})
	if err != nil {
		t.Fatalf("TriggerTyping: %v", err)
	}

	if gotPath != "/channels/999/typing" {
		t.Errorf("path = %q, want /channels/999/typing", gotPath)
	}
	if gotAuth != "Bot BOT_TOKEN" {
		t.Errorf("auth = %q, want Bot BOT_TOKEN", gotAuth)
	}
}

func TestDiscordTriggerTyping_EmptyChannel(t *testing.T) {
	adapter := &discordAdapter{}
	err := adapter.TriggerTyping(context.Background(), ChannelBinding{ChannelID: ""})
	if err != nil {
		t.Errorf("expected nil for empty channel, got %v", err)
	}

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	adapter.httpClient = srv.Client()
	adapter.apiBase = srv.URL

	_ = adapter.TriggerTyping(context.Background(), ChannelBinding{ChannelID: ""})
	if called {
		t.Error("should not call server for empty channel")
	}
}

func TestDiscordTriggerTyping_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message": "Missing Access"}`))
	}))
	defer srv.Close()

	adapter := &discordAdapter{
		httpClient: srv.Client(),
		token:      "BAD_TOKEN",
		apiBase:    srv.URL,
	}

	// Discord returns 403 but TriggerTyping doesn't treat it as a Go error,
	// just logs it. Verify it doesn't panic.
	err := adapter.TriggerTyping(context.Background(), ChannelBinding{ChannelID: "ch"})
	if err != nil {
		t.Errorf("expected nil (error is logged, not returned), got %v", err)
	}
}

// ============================================================
// Feishu TriggerTyping
// ============================================================

func TestFeishuTriggerTyping_PostsReaction(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":0}`))
	}))
	defer srv.Close()

	adapter := &feishuAdapter{
		httpClient: srv.Client(),
		domain:     "feishu",
		token:      "tenant_token_xxx",
	}
	// Override resolveAPIBase by pointing httpClient transport at test server.
	// Since resolveAPIBase returns a hardcoded URL, we use a custom transport
	// that rewrites the URL to our test server.
	origTransport := srv.Client().Transport
	_ = origTransport
	adapter.httpClient = &http.Client{
		Transport: urlRewriteTransport{
			targets:   []string{"https://open.feishu.cn"},
			rewriteTo: srv.URL,
			transport: http.DefaultTransport,
		},
	}

	err := adapter.TriggerTyping(context.Background(), ChannelBinding{
		ChannelID:            "ch-1",
		LastInboundMessageID: "msg-1",
	})
	if err != nil {
		t.Fatalf("TriggerTyping: %v", err)
	}

	if !strings.Contains(gotPath, "/im/v1/messages/msg-1/reactions") {
		t.Errorf("path = %q, want .../messages/msg-1/reactions", gotPath)
	}
	if gotAuth != "Bearer tenant_token_xxx" {
		t.Errorf("auth = %q", gotAuth)
	}
	// Verify reaction_type.emoji_type = "Typing"
	rt, _ := gotBody["reaction_type"].(map[string]any)
	if rt["emoji_type"] != "Typing" {
		t.Errorf("reaction_type.emoji_type = %v, want Typing", rt["emoji_type"])
	}
}

func TestFeishuTriggerTyping_FallsBackToOutboundMessageID(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":0}`))
	}))
	defer srv.Close()

	adapter := &feishuAdapter{
		httpClient: &http.Client{
			Transport: urlRewriteTransport{
				targets:   []string{"https://open.feishu.cn"},
				rewriteTo: srv.URL,
				transport: http.DefaultTransport,
			},
		},
		domain: "feishu",
		token:  "token",
	}

	err := adapter.TriggerTyping(context.Background(), ChannelBinding{
		ChannelID:             "ch-1",
		LastInboundMessageID:  "",
		LastOutboundMessageID: "out-msg-1",
	})
	if err != nil {
		t.Fatalf("TriggerTyping: %v", err)
	}

	if !strings.Contains(gotPath, "/messages/out-msg-1/reactions") {
		t.Errorf("path = %q, should target outbound message ID", gotPath)
	}
}

func TestFeishuTriggerTyping_NoMessageID(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	adapter := &feishuAdapter{
		httpClient: srv.Client(),
		domain:     "feishu",
		token:      "token",
	}

	err := adapter.TriggerTyping(context.Background(), ChannelBinding{
		ChannelID: "ch-1",
		// No message IDs
	})
	if err != nil {
		t.Fatalf("TriggerTyping: %v", err)
	}
	if called {
		t.Error("should not call server when no message ID available")
	}
}

func TestFeishuTriggerTyping_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":99991663,"msg":"message not found"}`))
	}))
	defer srv.Close()

	adapter := &feishuAdapter{
		httpClient: srv.Client(),
		domain:     "feishu",
		token:      "token",
	}

	// Should not return error (logged only).
	err := adapter.TriggerTyping(context.Background(), ChannelBinding{
		ChannelID:            "ch",
		LastInboundMessageID: "msg",
	})
	if err != nil {
		t.Errorf("expected nil (error logged, not returned), got %v", err)
	}
}

// ============================================================
// Slack TriggerTyping
// ============================================================

func TestSlackTriggerTyping_AddsEyesReaction(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	adapter := &slackAdapter{
		httpClient: &http.Client{
			Transport: urlRewriteTransport{
				targets:   []string{"https://slack.com"},
				rewriteTo: srv.URL,
				transport: http.DefaultTransport,
			},
		},
		botToken: "xoxb-test-token",
	}

	err := adapter.TriggerTyping(context.Background(), ChannelBinding{
		ChannelID:            "C12345",
		LastInboundMessageID: "1234567890.123456",
	})
	if err != nil {
		t.Fatalf("TriggerTyping: %v", err)
	}

	if gotBody["channel"] != "C12345" {
		t.Errorf("channel = %v, want C12345", gotBody["channel"])
	}
	if gotBody["timestamp"] != "1234567890.123456" {
		t.Errorf("timestamp = %v, want 1234567890.123456", gotBody["timestamp"])
	}
	if gotBody["name"] != "eyes" {
		t.Errorf("name = %v, want eyes", gotBody["name"])
	}
}

func TestSlackTriggerTyping_EmptyChannel(t *testing.T) {
	adapter := &slackAdapter{}
	err := adapter.TriggerTyping(context.Background(), ChannelBinding{ChannelID: ""})
	if err != nil {
		t.Errorf("expected nil for empty channel, got %v", err)
	}
}

func TestSlackTriggerTyping_NoMessageID(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	adapter := &slackAdapter{
		httpClient: srv.Client(),
		botToken:   "token",
	}

	err := adapter.TriggerTyping(context.Background(), ChannelBinding{
		ChannelID: "C123",
		// No message ID
	})
	if err != nil {
		t.Fatalf("TriggerTyping: %v", err)
	}
	if called {
		t.Error("should not call server when no message ID")
	}
}

func TestSlackTriggerTyping_AlreadyReacted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":false,"error":"already_reacted"}`))
	}))
	defer srv.Close()

	adapter := &slackAdapter{
		httpClient: &http.Client{
			Transport: urlRewriteTransport{
				targets:   []string{"https://slack.com"},
				rewriteTo: srv.URL,
				transport: http.DefaultTransport,
			},
		},
		botToken: "token",
	}

	// "already_reacted" is not an error — should return nil.
	err := adapter.TriggerTyping(context.Background(), ChannelBinding{
		ChannelID:            "C123",
		LastInboundMessageID: "ts-1",
	})
	if err != nil {
		t.Errorf("already_reacted should be ignored, got %v", err)
	}
}

func TestSlackTriggerTyping_OtherError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":false,"error":"not_in_channel"}`))
	}))
	defer srv.Close()

	adapter := &slackAdapter{
		httpClient: srv.Client(),
		botToken:   "token",
	}

	// Other errors are logged but not returned.
	err := adapter.TriggerTyping(context.Background(), ChannelBinding{
		ChannelID:            "C123",
		LastInboundMessageID: "ts-1",
	})
	if err != nil {
		t.Errorf("expected nil (logged only), got %v", err)
	}
}

// ============================================================
// QQ TriggerTyping
// ============================================================

func TestQQTriggerTyping_C2COnly(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		// QQ ensureToken calls /app/getAppAccessToken first
		if strings.Contains(r.URL.Path, "getAppAccessToken") {
			_, _ = w.Write([]byte(`{"access_token":"fake_token","expires_in":3600}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	adapter := &qqAdapter{
		httpClient: &http.Client{
			Transport: urlRewriteTransport{
				targets:   []string{"https://api.sgroup.qq.com", "https://bots.qq.com"},
				rewriteTo: srv.URL,
				transport: http.DefaultTransport,
			},
		},
		appID:     "test-app",
		appSecret: "test-secret",
	}
	adapter.chatTypes = map[string]string{"user-1": "c2c"}

	err := adapter.TriggerTyping(context.Background(), ChannelBinding{
		ChannelID:            "user-1",
		LastInboundMessageID: "msg-1",
	})
	if err != nil {
		t.Fatalf("TriggerTyping: %v", err)
	}

	if gotBody == nil {
		t.Fatal("expected request body")
	}
	if gotBody["msg_type"] != float64(6) {
		t.Errorf("msg_type = %v, want 6", gotBody["msg_type"])
	}
	notify, _ := gotBody["input_notify"].(map[string]any)
	if notify["input_type"] != float64(1) {
		t.Errorf("input_type = %v, want 1", notify["input_type"])
	}
	if notify["input_second"] != float64(60) {
		t.Errorf("input_second = %v, want 60", notify["input_second"])
	}
}

func TestQQTriggerTyping_GroupChatSkipped(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	adapter := &qqAdapter{
		httpClient: srv.Client(),
		appID:      "test-app",
		token:      "test-token",
	}
	adapter.chatTypes = map[string]string{"group-1": "group"}

	err := adapter.TriggerTyping(context.Background(), ChannelBinding{
		ChannelID: "group-1",
	})
	if err != nil {
		t.Fatalf("TriggerTyping: %v", err)
	}
	if called {
		t.Error("should not call server for group chats")
	}
}

func TestQQTriggerTyping_EmptyChannel(t *testing.T) {
	adapter := &qqAdapter{}
	err := adapter.TriggerTyping(context.Background(), ChannelBinding{ChannelID: ""})
	if err != nil {
		t.Errorf("expected nil for empty channel, got %v", err)
	}
}

func TestQQTriggerTyping_UnknownChatTypeSkipped(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	adapter := &qqAdapter{
		httpClient: srv.Client(),
		appID:      "test-app",
		token:      "test-token",
	}
	// No chatTypes entry — treated as non-C2C, should skip.

	err := adapter.TriggerTyping(context.Background(), ChannelBinding{
		ChannelID: "unknown-ch",
	})
	if err != nil {
		t.Fatalf("TriggerTyping: %v", err)
	}
	if called {
		t.Error("should not call server for unknown chat type")
	}
}

func TestQQTriggerTyping_SeqFromPassiveReplyCount(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		if strings.Contains(r.URL.Path, "getAppAccessToken") {
			_, _ = w.Write([]byte(`{"access_token":"fake_token","expires_in":3600}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	adapter := &qqAdapter{
		httpClient: &http.Client{
			Transport: urlRewriteTransport{
				targets:   []string{"https://api.sgroup.qq.com", "https://bots.qq.com"},
				rewriteTo: srv.URL,
				transport: http.DefaultTransport,
			},
		},
		appID:     "test-app",
		appSecret: "test-secret",
	}
	adapter.chatTypes = map[string]string{"user-1": "c2c"}

	err := adapter.TriggerTyping(context.Background(), ChannelBinding{
		ChannelID:            "user-1",
		LastInboundMessageID: "msg-1",
		PassiveReplyCount:    3,
	})
	if err != nil {
		t.Fatalf("TriggerTyping: %v", err)
	}

	if gotBody["msg_seq"] != float64(4) { // 3 + 1
		t.Errorf("msg_seq = %v, want 4 (PassiveReplyCount + 1)", gotBody["msg_seq"])
	}
}

// ============================================================
// LastMessageID helper
// ============================================================

func TestLastMessageID_PrefersInbound(t *testing.T) {
	got := LastMessageID(ChannelBinding{
		LastInboundMessageID:  "in-1",
		LastOutboundMessageID: "out-1",
	})
	if got != "in-1" {
		t.Errorf("LastMessageID = %q, want in-1", got)
	}
}

func TestLastMessageID_FallsBackToOutbound(t *testing.T) {
	got := LastMessageID(ChannelBinding{
		LastInboundMessageID:  "",
		LastOutboundMessageID: "out-1",
	})
	if got != "out-1" {
		t.Errorf("LastMessageID = %q, want out-1", got)
	}
}

func TestLastMessageID_BothEmpty(t *testing.T) {
	got := LastMessageID(ChannelBinding{})
	if got != "" {
		t.Errorf("LastMessageID = %q, want empty", got)
	}
}

// ============================================================
// RecordOutboundMessage
// ============================================================

func TestRecordOutboundMessage(t *testing.T) {
	mgr := NewManager()
	_ = mgr.SetBindingStore(NewMemoryBindingStore())
	mgr.BindSession(SessionBinding{SessionID: "s1", Workspace: "/tmp/project"})
	_, _ = mgr.BindChannel(ChannelBinding{
		Platform:  PlatformQQ,
		Adapter:   "qq",
		ChannelID: "ch-1",
	})

	err := mgr.RecordOutboundMessage("/tmp/project", "qq", "out-msg-1")
	if err != nil {
		t.Fatalf("RecordOutboundMessage: %v", err)
	}

	binding := mgr.CurrentBinding()
	if binding == nil {
		t.Fatal("expected binding")
	}
	if binding.LastOutboundMessageID != "out-msg-1" {
		t.Errorf("LastOutboundMessageID = %q, want out-msg-1", binding.LastOutboundMessageID)
	}
}

func TestRecordOutboundMessage_EmptyValues(t *testing.T) {
	mgr := NewManager()
	_ = mgr.SetBindingStore(NewMemoryBindingStore())
	mgr.BindSession(SessionBinding{SessionID: "s1", Workspace: "/tmp/project"})
	_, _ = mgr.BindChannel(ChannelBinding{
		Platform:  PlatformQQ,
		Adapter:   "qq",
		ChannelID: "ch-1",
	})

	// Empty message ID should be silently ignored.
	err := mgr.RecordOutboundMessage("/tmp/project", "qq", "")
	if err != nil {
		t.Fatalf("RecordOutboundMessage: %v", err)
	}

	// Empty adapter should be silently ignored.
	err = mgr.RecordOutboundMessage("/tmp/project", "", "msg-1")
	if err != nil {
		t.Fatalf("RecordOutboundMessage: %v", err)
	}
}

func TestRecordOutboundMessage_WrongAdapter(t *testing.T) {
	mgr := NewManager()
	_ = mgr.SetBindingStore(NewMemoryBindingStore())
	mgr.BindSession(SessionBinding{SessionID: "s1", Workspace: "/tmp/project"})
	_, _ = mgr.BindChannel(ChannelBinding{
		Platform:  PlatformQQ,
		Adapter:   "qq",
		ChannelID: "ch-1",
	})

	// Adapter "tg" is not bound — should be silently ignored.
	err := mgr.RecordOutboundMessage("/tmp/project", "tg", "msg-1")
	if err != nil {
		t.Fatalf("RecordOutboundMessage: %v", err)
	}

	binding := mgr.CurrentBinding()
	if binding.LastOutboundMessageID != "" {
		t.Errorf("LastOutboundMessageID should be empty, got %q", binding.LastOutboundMessageID)
	}
}

// ============================================================
// helpers
// ============================================================

// urlRewriteTransport rewrites request URLs from hardcoded targets to a
// test server. Used for adapters (feishu, slack, QQ) that construct URLs
// from hardcoded constants rather than a configurable apiBase field.
type urlRewriteTransport struct {
	targets   []string // URLs to rewrite (tried in order)
	rewriteTo string   // e.g. test server URL
	transport http.RoundTripper
}

func (t urlRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for _, target := range t.targets {
		if strings.HasPrefix(req.URL.String(), target) {
			newURL := t.rewriteTo + strings.TrimPrefix(req.URL.String(), target)
			newReq := req.Clone(req.Context())
			newReq.URL, _ = url.Parse(newURL)
			return t.transport.RoundTrip(newReq)
		}
	}
	return t.transport.RoundTrip(req)
}
