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

	"maunium.net/go/mautrix"
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

func TestReactionAckState_DeduplicatesPerBinding(t *testing.T) {
	var state reactionAckState
	binding := ChannelBinding{Workspace: "/tmp/project", ChannelID: "ch-1"}
	if !state.NeedsSend(binding, "msg-1") {
		t.Fatal("first target should need send")
	}
	state.MarkSent(binding, "msg-1")
	if state.NeedsSend(binding, "msg-1") {
		t.Fatal("same target on same binding should be deduplicated")
	}
	if !state.NeedsSend(binding, "msg-2") {
		t.Fatal("new target should need send")
	}
	otherBinding := ChannelBinding{Workspace: "/tmp/project", ChannelID: "ch-2"}
	if !state.NeedsSend(otherBinding, "msg-1") {
		t.Fatal("same target on different binding should still need send")
	}
}

func TestReactionAckValue_IsStablePerTarget(t *testing.T) {
	first := reactionAckValue(PlatformDiscord, "msg-1")
	if first == "" {
		t.Fatal("expected reaction for discord")
	}
	if got := reactionAckValue(PlatformDiscord, "msg-1"); got != first {
		t.Fatalf("reaction should be stable for same target: %q vs %q", first, got)
	}
	if got := reactionAckValue(PlatformFeishu, "msg-1"); got != "Typing" {
		t.Fatalf("feishu reaction = %q, want Typing", got)
	}
}

// ============================================================
// Telegram TriggerTyping
// ============================================================

func TestTGTriggerTyping_SetsReactionWhenMessageIDAvailable(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
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

	binding := ChannelBinding{ChannelID: "12345", LastInboundMessageID: "777"}
	if err := adapter.TriggerTyping(context.Background(), binding); err != nil {
		t.Fatalf("TriggerTyping: %v", err)
	}
	if err := adapter.TriggerTyping(context.Background(), binding); err != nil {
		t.Fatalf("TriggerTyping second call: %v", err)
	}

	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if !strings.Contains(gotPath, "/botTEST_TOKEN/setMessageReaction") {
		t.Errorf("path = %q, want /botTEST_TOKEN/setMessageReaction", gotPath)
	}
	if gotBody["chat_id"] != "12345" {
		t.Errorf("chat_id = %v, want 12345", gotBody["chat_id"])
	}
	if gotBody["message_id"] != float64(777) {
		t.Errorf("message_id = %v, want 777", gotBody["message_id"])
	}
	reactions, _ := gotBody["reaction"].([]any)
	if len(reactions) != 1 {
		t.Fatalf("reaction len = %d, want 1", len(reactions))
	}
	reaction, _ := reactions[0].(map[string]any)
	if reaction["type"] != "emoji" || reaction["emoji"] != "👍" {
		t.Errorf("reaction = %#v, want emoji 👍", reaction)
	}
}

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

func TestDiscordTriggerTyping_AddsReactionWhenMessageIDAvailable(t *testing.T) {
	var gotPath string
	var gotMethod string
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotPath = r.URL.EscapedPath()
		gotMethod = r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	adapter := &discordAdapter{
		httpClient: srv.Client(),
		token:      "BOT_TOKEN",
		apiBase:    srv.URL,
	}

	binding := ChannelBinding{ChannelID: "999", LastInboundMessageID: "msg-1"}
	if err := adapter.TriggerTyping(context.Background(), binding); err != nil {
		t.Fatalf("TriggerTyping: %v", err)
	}
	if err := adapter.TriggerTyping(context.Background(), binding); err != nil {
		t.Fatalf("TriggerTyping second call: %v", err)
	}

	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if gotMethod != http.MethodPut {
		t.Fatalf("method = %s, want PUT", gotMethod)
	}
	wantPath := "/channels/999/messages/msg-1/reactions/" + url.PathEscape(reactionAckValue(PlatformDiscord, "msg-1")) + "/@me"
	if gotPath != wantPath {
		t.Errorf("path = %q, want %q", gotPath, wantPath)
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

func TestFeishuTriggerTyping_PrefersLatestInboundMessageID(t *testing.T) {
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
		LastInboundMessageID:  "in-msg-1",
		LastOutboundMessageID: "out-msg-1",
	})
	if err != nil {
		t.Fatalf("TriggerTyping: %v", err)
	}

	if !strings.Contains(gotPath, "/messages/in-msg-1/reactions") {
		t.Errorf("path = %q, should target latest inbound message ID", gotPath)
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
		httpClient: &http.Client{
			Transport: urlRewriteTransport{
				targets:   []string{"https://open.feishu.cn/open-apis", "https://open.larksuite.com/open-apis"},
				rewriteTo: srv.URL,
				transport: http.DefaultTransport,
			},
		},
		domain: "feishu",
		token:  "token",
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
		httpClient: &http.Client{
			Transport: urlRewriteTransport{
				targets:   []string{"https://open.feishu.cn/open-apis", "https://open.larksuite.com/open-apis"},
				rewriteTo: srv.URL,
				transport: http.DefaultTransport,
			},
		},
		domain: "feishu",
		token:  "token",
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
	wantReaction := reactionAckValue(PlatformSlack, "1234567890.123456")
	if gotBody["name"] != wantReaction {
		t.Errorf("name = %v, want %s", gotBody["name"], wantReaction)
	}
}

func TestSlackTriggerTyping_DeduplicatesReactionPerMessage(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
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

	binding := ChannelBinding{ChannelID: "C12345", LastInboundMessageID: "1234567890.123456"}
	if err := adapter.TriggerTyping(context.Background(), binding); err != nil {
		t.Fatalf("TriggerTyping: %v", err)
	}
	if err := adapter.TriggerTyping(context.Background(), binding); err != nil {
		t.Fatalf("TriggerTyping second call: %v", err)
	}

	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestSlackTriggerTyping_PrefersLatestInboundMessageID(t *testing.T) {
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
		ChannelID:             "C12345",
		LastInboundMessageID:  "1234567890.123456",
		LastOutboundMessageID: "9999999999.999999",
	})
	if err != nil {
		t.Fatalf("TriggerTyping: %v", err)
	}

	if gotBody["timestamp"] != "1234567890.123456" {
		t.Errorf("timestamp = %v, want latest inbound message id", gotBody["timestamp"])
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
		httpClient: srv.Client(),
		botToken:   "token",
		apiBase:    srv.URL,
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
		apiBase:    srv.URL,
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
// Matrix TriggerTyping
// ============================================================

func TestMatrixTriggerTyping_SendsReaction(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotPath = r.URL.EscapedPath()
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"event_id":"$reaction-1"}`))
	}))
	defer srv.Close()

	client, err := mautrix.NewClient(srv.URL, "", "token")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	adapter := &matrixAdapter{client: client}

	binding := ChannelBinding{ChannelID: "!room:example", LastInboundMessageID: "$event-1"}
	if err := adapter.TriggerTyping(context.Background(), binding); err != nil {
		t.Fatalf("TriggerTyping: %v", err)
	}
	if err := adapter.TriggerTyping(context.Background(), binding); err != nil {
		t.Fatalf("TriggerTyping second call: %v", err)
	}

	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if !strings.Contains(gotPath, "/send/m.reaction/") {
		t.Fatalf("path = %q, want m.reaction send path", gotPath)
	}
	relatesTo, _ := gotBody["m.relates_to"].(map[string]any)
	if relatesTo["event_id"] != "$event-1" {
		t.Errorf("event_id = %v, want $event-1", relatesTo["event_id"])
	}
	if relatesTo["rel_type"] != "m.annotation" {
		t.Errorf("rel_type = %v, want m.annotation", relatesTo["rel_type"])
	}
	wantReaction := reactionAckValue(PlatformMatrix, "$event-1")
	if relatesTo["key"] != wantReaction {
		t.Errorf("key = %v, want %s", relatesTo["key"], wantReaction)
	}
}

// ============================================================
// Mattermost TriggerTyping
// ============================================================

func TestMattermostTriggerTyping_PostsReaction(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotBody map[string]any
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	adapter := &mattermostAdapter{
		conn:      srv.Client(),
		baseURL:   srv.URL,
		token:     "token",
		botUserID: "bot-user",
	}

	binding := ChannelBinding{ChannelID: "channel-1", LastInboundMessageID: "post-1"}
	if err := adapter.TriggerTyping(context.Background(), binding); err != nil {
		t.Fatalf("TriggerTyping: %v", err)
	}
	if err := adapter.TriggerTyping(context.Background(), binding); err != nil {
		t.Fatalf("TriggerTyping second call: %v", err)
	}

	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if gotPath != "/api/v4/reactions" {
		t.Errorf("path = %q, want /api/v4/reactions", gotPath)
	}
	if gotAuth != "Bearer token" {
		t.Errorf("auth = %q, want Bearer token", gotAuth)
	}
	if gotBody["user_id"] != "bot-user" {
		t.Errorf("user_id = %v, want bot-user", gotBody["user_id"])
	}
	if gotBody["post_id"] != "post-1" {
		t.Errorf("post_id = %v, want post-1", gotBody["post_id"])
	}
	wantReaction := reactionAckValue(PlatformMattermost, "post-1")
	if gotBody["emoji_name"] != wantReaction {
		t.Errorf("emoji_name = %v, want %s", gotBody["emoji_name"], wantReaction)
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

func TestLastMessageID_PrefersOutbound(t *testing.T) {
	got := LastMessageID(ChannelBinding{
		LastInboundMessageID:  "in-1",
		LastOutboundMessageID: "out-1",
	})
	if got != "out-1" {
		t.Errorf("LastMessageID = %q, want out-1", got)
	}
}

func TestLastMessageID_FallsBackToInbound(t *testing.T) {
	got := LastMessageID(ChannelBinding{
		LastInboundMessageID:  "in-1",
		LastOutboundMessageID: "",
	})
	if got != "in-1" {
		t.Errorf("LastMessageID = %q, want in-1", got)
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

func TestSlackSend_RecordsOutboundMessageID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"ts":"1234567890.654321"}`))
	}))
	defer srv.Close()

	mgr := NewManager()
	_ = mgr.SetBindingStore(NewMemoryBindingStore())
	mgr.BindSession(SessionBinding{SessionID: "s1", Workspace: "/tmp/project"})
	binding, _ := mgr.BindChannel(ChannelBinding{
		Workspace: "/tmp/project",
		Platform:  PlatformSlack,
		Adapter:   "slack",
		ChannelID: "C123",
	})

	adapter := &slackAdapter{
		manager:    mgr,
		httpClient: srv.Client(),
		botToken:   "xoxb-test-token",
		apiBase:    srv.URL,
		connected:  true,
	}

	if err := adapter.Send(context.Background(), binding, OutboundEvent{Kind: OutboundEventText, Text: "hello"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	updated := mgr.CurrentBinding()
	if updated == nil || updated.LastOutboundMessageID != "1234567890.654321" {
		t.Fatalf("LastOutboundMessageID = %q, want 1234567890.654321", updated.LastOutboundMessageID)
	}
}

func TestFeishuSend_RecordsOutboundMessageID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":0,"data":{"message_id":"om_dc13264520392913993dd051dba21dcf"}}`))
	}))
	defer srv.Close()

	mgr := NewManager()
	_ = mgr.SetBindingStore(NewMemoryBindingStore())
	mgr.BindSession(SessionBinding{SessionID: "s1", Workspace: "/tmp/project"})
	binding, _ := mgr.BindChannel(ChannelBinding{
		Workspace: "/tmp/project",
		Platform:  PlatformFeishu,
		Adapter:   "feishu",
		ChannelID: "oc_test_chat",
	})

	adapter := &feishuAdapter{
		manager: mgr,
		httpClient: &http.Client{
			Transport: urlRewriteTransport{
				targets:   []string{"https://open.feishu.cn"},
				rewriteTo: srv.URL,
				transport: http.DefaultTransport,
			},
		},
		domain:    "feishu",
		token:     "tenant_token_xxx",
		connected: true,
	}

	if err := adapter.Send(context.Background(), binding, OutboundEvent{Kind: OutboundEventText, Text: "hello"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	updated := mgr.CurrentBinding()
	if updated == nil || updated.LastOutboundMessageID != "om_dc13264520392913993dd051dba21dcf" {
		t.Fatalf("LastOutboundMessageID = %q, want om_dc13264520392913993dd051dba21dcf", updated.LastOutboundMessageID)
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
