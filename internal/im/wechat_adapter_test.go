package im

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestNewWechatAdapter_DefaultBaseURL(t *testing.T) {
	a, err := newWechatAdapter("test", config.IMConfig{}, config.IMAdapterConfig{Extra: map[string]any{}}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.baseURL != ilinkBaseURL {
		t.Errorf("expected default baseURL %q, got %q", ilinkBaseURL, a.baseURL)
	}
	if a.Name() != "test" {
		t.Errorf("expected name 'test', got %q", a.Name())
	}
}

func TestNewWechatAdapter_CustomBaseURL(t *testing.T) {
	a, err := newWechatAdapter("wc", config.IMConfig{}, config.IMAdapterConfig{
		Extra: map[string]any{"base_url": "https://custom.ilink.example.com"},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.baseURL != "https://custom.ilink.example.com" {
		t.Errorf("expected custom baseURL, got %q", a.baseURL)
	}
}

func TestNewWechatAdapter_InitialToken(t *testing.T) {
	a, err := newWechatAdapter("wc", config.IMConfig{}, config.IMAdapterConfig{
		Extra: map[string]any{"bot_token": "preconfigured-token"},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.botToken != "preconfigured-token" {
		t.Errorf("expected bot_token to be 'preconfigured-token', got %q", a.botToken)
	}
}

func TestWechatAdapter_AuthenticateQRCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ilinkGetQRCodePath {
			t.Errorf("expected path %q, got %q", ilinkGetQRCodePath, r.URL.Path)
			http.Error(w, "not found", 404)
			return
		}
		if r.URL.Query().Get("bot_type") != "3" {
			t.Errorf("expected bot_type=3, got %q", r.URL.Query().Get("bot_type"))
		}
		if r.Header.Get("AuthorizationType") != "ilink_bot_token" {
			t.Errorf("expected AuthorizationType header 'ilink_bot_token', got %q", r.Header.Get("AuthorizationType"))
		}
		resp := ilinkQRCodeResponse{
			QRCode:           "test-qrcode-token-123",
			QRCodeImgContent: "https://login.weixin.qq.com/l/test123",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	a, _ := newWechatAdapter("wc", config.IMConfig{}, config.IMAdapterConfig{
		Extra: map[string]any{"base_url": srv.URL},
	}, nil)

	qrcodeToken, imgBase64, err := a.AuthenticateQRCode(context.Background())
	if err != nil {
		t.Fatalf("AuthenticateQRCode error: %v", err)
	}
	if qrcodeToken != "test-qrcode-token-123" {
		t.Errorf("expected qrcode token, got %q", qrcodeToken)
	}
	if imgBase64 == "" {
		t.Error("expected non-empty img content")
	}
}

func TestWechatAdapter_PollQRCodeStatus_Confirmed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ilinkQRCodeStatusResponse{
			Status:   "confirmed",
			BotToken: "new-bot-token-abc",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	a, _ := newWechatAdapter("wc", config.IMConfig{}, config.IMAdapterConfig{
		Extra: map[string]any{"base_url": srv.URL},
	}, nil)

	status, botToken, err := a.PollQRCodeStatus(context.Background(), "qr-token")
	if err != nil {
		t.Fatalf("PollQRCodeStatus error: %v", err)
	}
	if status != "confirmed" {
		t.Errorf("expected status 'confirmed', got %q", status)
	}
	if botToken != "new-bot-token-abc" {
		t.Errorf("expected bot token, got %q", botToken)
	}
}

func TestWechatAdapter_PollQRCodeStatus_Waiting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ilinkQRCodeStatusResponse{Status: "wait"}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	a, _ := newWechatAdapter("wc", config.IMConfig{}, config.IMAdapterConfig{
		Extra: map[string]any{"base_url": srv.URL},
	}, nil)

	status, _, err := a.PollQRCodeStatus(context.Background(), "qr-token")
	if err != nil {
		t.Fatalf("PollQRCodeStatus error: %v", err)
	}
	if status != "wait" {
		t.Errorf("expected status 'wait', got %q", status)
	}
}

func TestWechatAdapter_SetBotToken(t *testing.T) {
	a, _ := newWechatAdapter("wc", config.IMConfig{}, config.IMAdapterConfig{}, nil)
	if a.botToken != "" {
		t.Errorf("expected empty initial token")
	}
	a.SetBotToken("my-new-token")
	if a.botToken != "my-new-token" {
		t.Errorf("expected token 'my-new-token', got %q", a.botToken)
	}
}

func TestWechatAdapter_Send_NoToken(t *testing.T) {
	a, _ := newWechatAdapter("wc", config.IMConfig{}, config.IMAdapterConfig{}, nil)
	err := a.Send(context.Background(), ChannelBinding{}, OutboundEvent{Kind: OutboundEventText, Text: "hello"})
	if err == nil {
		t.Fatal("expected error when bot_token is empty")
	}
	if !strings.Contains(err.Error(), "no bot_token") {
		t.Errorf("expected 'no bot_token' error, got: %v", err)
	}
}

func TestWechatAdapter_Send_Success(t *testing.T) {
	var receivedBody ilinkSendMessageRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("expected Bearer token header, got %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("AuthorizationType") != "ilink_bot_token" {
			t.Errorf("expected AuthorizationType header")
		}
		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ret":0}`)
	}))
	defer srv.Close()

	a, _ := newWechatAdapter("wc", config.IMConfig{}, config.IMAdapterConfig{
		Extra: map[string]any{
			"base_url":  srv.URL,
			"bot_token": "test-token",
		},
	}, nil)

	err := a.Send(context.Background(), ChannelBinding{
		ChannelID: "user-123",
	}, OutboundEvent{Kind: OutboundEventText, Text: "Hello WeChat!"})
	if err != nil {
		t.Fatalf("Send error: %v", err)
	}

	if receivedBody.Msg.ToUserID != "user-123" {
		t.Errorf("expected ToUserID 'user-123', got %q", receivedBody.Msg.ToUserID)
	}
	if receivedBody.Msg.ClientID == "" {
		t.Error("expected ClientID to be set")
	}
	if receivedBody.Msg.MessageType != ilinkMsgTypeBot {
		t.Errorf("expected MessageType=%d, got %d", ilinkMsgTypeBot, receivedBody.Msg.MessageType)
	}
	if receivedBody.BaseInfo.ChannelVersion != "2.0.0" {
		t.Errorf("expected BaseInfo.ChannelVersion '2.0.0', got %q", receivedBody.BaseInfo.ChannelVersion)
	}
	if len(receivedBody.Msg.ItemList) != 1 {
		t.Fatalf("expected 1 item, got %d", len(receivedBody.Msg.ItemList))
	}
	if receivedBody.Msg.ItemList[0].TextItem == nil || receivedBody.Msg.ItemList[0].TextItem.Text != "Hello WeChat!" {
		t.Errorf("expected text item 'Hello WeChat!', got %+v", receivedBody.Msg.ItemList[0])
	}
}

func TestWechatAdapter_Send_EmptyText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not make HTTP request for empty text")
	}))
	defer srv.Close()

	a, _ := newWechatAdapter("wc", config.IMConfig{}, config.IMAdapterConfig{
		Extra: map[string]any{
			"base_url":  srv.URL,
			"bot_token": "test-token",
		},
	}, nil)

	err := a.Send(context.Background(), ChannelBinding{}, OutboundEvent{Kind: OutboundEventText, Text: ""})
	if err != nil {
		t.Fatalf("expected nil for empty text, got: %v", err)
	}
}

func TestWechatAdapter_Send_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	a, _ := newWechatAdapter("wc", config.IMConfig{}, config.IMAdapterConfig{
		Extra: map[string]any{
			"base_url":  srv.URL,
			"bot_token": "test-token",
		},
	}, nil)

	err := a.Send(context.Background(), ChannelBinding{TargetID: "user-123"}, OutboundEvent{Kind: OutboundEventText, Text: "test"})
	if err == nil {
		t.Fatal("expected error on HTTP 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500 in error, got: %v", err)
	}
}

func TestWechatAdapter_Start_NoToken(t *testing.T) {
	mgr := NewManager()
	a, _ := newWechatAdapter("wc", config.IMConfig{}, config.IMAdapterConfig{}, mgr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a.Start(ctx)
	// Should not crash, just log "waiting_for_auth" state
	time.Sleep(50 * time.Millisecond)
}

func TestWechatManager_WechatAdapter_Nil(t *testing.T) {
	mgr := NewManager()
	if mgr.WechatAdapter() != nil {
		t.Fatal("expected nil when no wechat adapter registered")
	}
}

func TestWechatManager_WechatAdapter_Found(t *testing.T) {
	mgr := NewManager()
	a, _ := newWechatAdapter("wc", config.IMConfig{}, config.IMAdapterConfig{}, mgr)
	mgr.sinks["wc"] = a

	found := mgr.WechatAdapter()
	if found == nil {
		t.Fatal("expected to find wechat adapter")
	}
	if found.Name() != "wc" {
		t.Errorf("expected name 'wc', got %q", found.Name())
	}
}

func TestWechatAdapter_CommonHeaders(t *testing.T) {
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ret":0}`)
	}))
	defer srv.Close()

	a, _ := newWechatAdapter("wc", config.IMConfig{}, config.IMAdapterConfig{
		Extra: map[string]any{
			"base_url":  srv.URL,
			"bot_token": "bearer-token-xyz",
		},
	}, nil)

	_ = a.Send(context.Background(), ChannelBinding{TargetID: "u"}, OutboundEvent{Kind: OutboundEventText, Text: "hi"})

	if gotHeaders.Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", gotHeaders.Get("Content-Type"))
	}
	if gotHeaders.Get("AuthorizationType") != "ilink_bot_token" {
		t.Errorf("expected AuthorizationType, got %q", gotHeaders.Get("AuthorizationType"))
	}
	if gotHeaders.Get("X-WECHAT-UIN") == "" {
		t.Error("expected X-WECHAT-UIN header to be set")
	}
	if gotHeaders.Get("Authorization") != "Bearer bearer-token-xyz" {
		t.Errorf("expected Authorization Bearer, got %q", gotHeaders.Get("Authorization"))
	}
}

func TestWechatAdapter_Send_UsesBindingContextToken(t *testing.T) {
	var receivedBody ilinkSendMessageRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ret":0}`)
	}))
	defer srv.Close()

	a, _ := newWechatAdapter("wc", config.IMConfig{}, config.IMAdapterConfig{
		Extra: map[string]any{
			"base_url":  srv.URL,
			"bot_token": "test-token",
		},
	}, nil)

	// Send with ContextToken in binding
	err := a.Send(context.Background(), ChannelBinding{
		ChannelID:    "user-123",
		ContextToken: "saved-token-xyz",
	}, OutboundEvent{Kind: OutboundEventText, Text: "Hello"})
	if err != nil {
		t.Fatalf("Send error: %v", err)
	}

	if receivedBody.Msg.ContextToken != "saved-token-xyz" {
		t.Errorf("ContextToken: got %q, want %q", receivedBody.Msg.ContextToken, "saved-token-xyz")
	}
	if receivedBody.BaseInfo.ChannelVersion != "2.0.0" {
		t.Errorf("ChannelVersion: got %q", receivedBody.BaseInfo.ChannelVersion)
	}
}

func TestWechatAdapter_Send_EmptyContextToken(t *testing.T) {
	var receivedBody ilinkSendMessageRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ret":0}`)
	}))
	defer srv.Close()

	a, _ := newWechatAdapter("wc", config.IMConfig{}, config.IMAdapterConfig{
		Extra: map[string]any{
			"base_url":  srv.URL,
			"bot_token": "test-token",
		},
	}, nil)

	// Send without ContextToken (first boot, no inbound yet)
	err := a.Send(context.Background(), ChannelBinding{
		ChannelID: "user-123",
	}, OutboundEvent{Kind: OutboundEventText, Text: "Hello"})
	if err != nil {
		t.Fatalf("Send error: %v", err)
	}

	// Should still send (empty context_token) — WeChat will accept ~2 messages
	if receivedBody.Msg.ContextToken != "" {
		t.Errorf("ContextToken should be empty, got %q", receivedBody.Msg.ContextToken)
	}
}
