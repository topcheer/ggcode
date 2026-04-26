package im

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/topcheer/ggcode/internal/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestEnsureTokenAcceptsStringExpiresIn(t *testing.T) {
	adapter := &qqAdapter{
		appID:     "123",
		appSecret: "secret",
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.String() != qqTokenURL {
					t.Fatalf("unexpected request URL: %s", req.URL.String())
				}
				return jsonResponse(`{"access_token":"token-123","expires_in":"2820"}`), nil
			}),
		},
	}

	token, err := adapter.ensureToken(context.Background())
	if err != nil {
		t.Fatalf("ensureToken returned error: %v", err)
	}
	if token != "token-123" {
		t.Fatalf("unexpected token: %q", token)
	}
	if adapter.accessToken() != "token-123" {
		t.Fatalf("expected cached token, got %q", adapter.accessToken())
	}
}

func TestAPIRequestDoesNotSwallowTokenErrors(t *testing.T) {
	var gatewayCalls int
	adapter := &qqAdapter{
		appID:     "123",
		appSecret: "secret",
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				switch req.URL.String() {
				case qqTokenURL:
					return jsonResponse(`{"access_token":"token-123","expires_in":"bad-value"}`), nil
				case qqAPIBase + qqGatewayPath:
					gatewayCalls++
					return jsonResponse(`{"url":"wss://example.invalid"}`), nil
				default:
					t.Fatalf("unexpected request URL: %s", req.URL.String())
					return nil, nil
				}
			}),
		},
	}

	var payload map[string]any
	_, err := adapter.apiRequest(context.Background(), http.MethodGet, qqGatewayPath, nil, &payload)
	if err == nil || !strings.Contains(err.Error(), "parse QQ expires_in") {
		t.Fatalf("expected expires_in parse error, got %v", err)
	}
	if gatewayCalls != 0 {
		t.Fatalf("expected gateway call to be skipped after token failure, got %d", gatewayCalls)
	}
}

func TestGenerateShareLinkRefreshesExpiredServerTokenAndRetries(t *testing.T) {
	var shareCalls int
	adapter := &qqAdapter{
		appID:          "123",
		appSecret:      "secret",
		token:          "stale-token",
		tokenExpiresAt: time.Now().Add(time.Hour),
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				switch req.URL.String() {
				case qqTokenURL:
					return jsonResponse(`{"access_token":"fresh-token","expires_in":"3600"}`), nil
				case qqAPIBase + qqShareURLPath:
					shareCalls++
					if got := req.Header.Get("Authorization"); shareCalls == 1 {
						if got != "QQBot stale-token" {
							t.Fatalf("first share call used %q", got)
						}
						return &http.Response{
							StatusCode: http.StatusInternalServerError,
							Header:     make(http.Header),
							Body:       io.NopCloser(strings.NewReader(`{"message":"token not exist or expire","code":11244,"err_code":11244}`)),
						}, nil
					}
					if got := req.Header.Get("Authorization"); got != "QQBot fresh-token" {
						t.Fatalf("retry share call used %q", got)
					}
					return jsonResponse(`{"retcode":0,"msg":"success","data":{"url":"https://bot.q.qq.com/share/test"}}`), nil
				default:
					t.Fatalf("unexpected request URL: %s", req.URL.String())
					return nil, nil
				}
			}),
		},
	}

	link, err := adapter.GenerateShareLink(context.Background(), "workspace-alpha")
	if err != nil {
		t.Fatalf("GenerateShareLink returned error: %v", err)
	}
	if link != "https://bot.q.qq.com/share/test" {
		t.Fatalf("unexpected share link: %q", link)
	}
	if shareCalls != 2 {
		t.Fatalf("expected two share-link calls, got %d", shareCalls)
	}
	if adapter.accessToken() != "fresh-token" {
		t.Fatalf("expected refreshed token cache, got %q", adapter.accessToken())
	}
}

func TestGenerateShareLinkCallsQQAPI(t *testing.T) {
	adapter := &qqAdapter{
		appID:          "123",
		appSecret:      "secret",
		token:          "token-123",
		tokenExpiresAt: time.Now().Add(time.Hour),
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.String() != qqAPIBase+qqShareURLPath {
					t.Fatalf("unexpected request URL: %s", req.URL.String())
				}
				if req.Header.Get("Authorization") != "QQBot token-123" {
					t.Fatalf("unexpected authorization header: %q", req.Header.Get("Authorization"))
				}
				body, _ := io.ReadAll(req.Body)
				if !strings.Contains(string(body), `"callback_data":"workspace-alpha"`) {
					t.Fatalf("expected callback_data in request body, got %s", string(body))
				}
				return jsonResponse(`{"retcode":0,"msg":"success","data":{"url":"https://bot.q.qq.com/share/test"}}`), nil
			}),
		},
	}

	link, err := adapter.GenerateShareLink(context.Background(), "workspace-alpha")
	if err != nil {
		t.Fatalf("GenerateShareLink returned error: %v", err)
	}
	if link != "https://bot.q.qq.com/share/test" {
		t.Fatalf("unexpected share link: %q", link)
	}
}

func TestGenerateShareLinkAcceptsTopLevelURL(t *testing.T) {
	adapter := &qqAdapter{
		appID:          "123",
		appSecret:      "secret",
		token:          "token-123",
		tokenExpiresAt: time.Now().Add(time.Hour),
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return jsonResponse(`{"url":"https://bot.q.qq.com/share/top-level"}`), nil
			}),
		},
	}

	link, err := adapter.GenerateShareLink(context.Background(), "workspace-alpha")
	if err != nil {
		t.Fatalf("GenerateShareLink returned error: %v", err)
	}
	if link != "https://bot.q.qq.com/share/top-level" {
		t.Fatalf("unexpected share link: %q", link)
	}
}

func TestGenerateShareLinkRejectsMissingURL(t *testing.T) {
	adapter := &qqAdapter{
		appID:          "123",
		appSecret:      "secret",
		token:          "token-123",
		tokenExpiresAt: time.Now().Add(time.Hour),
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return jsonResponse(`{}`), nil
			}),
		},
	}

	link, err := adapter.GenerateShareLink(context.Background(), "workspace-alpha")
	if err == nil || !strings.Contains(err.Error(), "missing url") {
		t.Fatalf("expected missing url error, got link=%q err=%v", link, err)
	}
}

func TestNewQQAdapterDefaultsToC2C(t *testing.T) {
	adapter, err := newQQAdapter("hermes", config.IMConfig{}, config.IMAdapterConfig{
		Enabled:  true,
		Platform: string(PlatformQQ),
		Extra: map[string]interface{}{
			"appid":     "123",
			"appsecret": "secret",
		},
	}, nil)
	if err != nil {
		t.Fatalf("newQQAdapter returned error: %v", err)
	}
	if adapter.defaultChatType != "c2c" {
		t.Fatalf("expected default chat type c2c, got %q", adapter.defaultChatType)
	}
}

func TestQQMessagePathTreatsUsersAsC2C(t *testing.T) {
	if got := qqMessagePath("users", "openid-1"); got != "/v2/users/openid-1/messages" {
		t.Fatalf("unexpected users path: %q", got)
	}
	if got := qqMessagePath("c2c", "openid-1"); got != "/v2/users/openid-1/messages" {
		t.Fatalf("unexpected c2c path: %q", got)
	}
}

func TestHandleMessageEventStartsPairingForNewChannel(t *testing.T) {
	mgr := NewManager()
	bridge := &stubBridge{}
	mgr.SetBridge(bridge)
	store := NewMemoryBindingStore()
	_ = mgr.SetBindingStore(store)
	_ = mgr.SetPairingStore(NewMemoryPairingStore())
	mgr.BindSession(SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	if _, err := mgr.BindChannel(ChannelBinding{
		Platform:  PlatformQQ,
		Adapter:   "hermes",
		TargetID:  "ops",
		ChannelID: "group-1",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}

	var posts []string
	adapter := &qqAdapter{
		name:           "hermes",
		manager:        mgr,
		connected:      true,
		token:          "token-123",
		tokenExpiresAt: time.Now().Add(time.Hour),
		ws:             &websocket.Conn{},
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodPost {
				body, _ := io.ReadAll(req.Body)
				posts = append(posts, req.URL.Path+" "+string(body))
			}
			return jsonResponse(`{}`), nil
		})},
		chatTypes: map[string]string{},
		seen:      map[string]time.Time{},
	}

	adapter.handleMessageEvent(context.Background(), "GROUP_AT_MESSAGE_CREATE", map[string]any{
		"id":           "msg-1",
		"group_openid": "group-2",
		"content":      "@bot hello",
		"author": map[string]any{
			"member_openid": "user-2",
			"username":      "tester",
		},
	})

	if len(posts) != 1 {
		t.Fatalf("expected pairing reply, got %d requests", len(posts))
	}
	if !strings.Contains(posts[0], "/v2/groups/group-2/messages") || !strings.Contains(posts[0], "4 位绑定码") || !strings.Contains(posts[0], "\"msg_id\":\"msg-1\"") {
		t.Fatalf("unexpected pairing payload: %s", posts[0])
	}
	if bridge.last.Text != "" {
		t.Fatalf("expected pairing message not to reach bridge, got %#v", bridge.last)
	}
	if pending := mgr.Snapshot().PendingPairing; pending == nil || pending.ChannelID != "group-2" {
		t.Fatalf("expected pending pairing challenge, got %#v", pending)
	}
}

func TestProcessAttachmentsCachesImageAndFileLocally(t *testing.T) {
	pngData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
		0x89, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9C, 0x63, 0xF8, 0xCF, 0xC0, 0x00,
		0x00, 0x03, 0x01, 0x01, 0x00, 0x18, 0xDD, 0x8D,
		0x18, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E,
		0x44, 0xAE, 0x42, 0x60, 0x82,
	}
	fileData := []byte("hello from qq attachment")
	adapter := &qqAdapter{
		token:          "token-123",
		tokenExpiresAt: time.Now().Add(time.Hour),
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.String() {
			case "https://example.com/image":
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"image/png"}},
					Body:       io.NopCloser(strings.NewReader(string(pngData))),
				}, nil
			case "https://example.com/file":
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/pdf"}},
					Body:       io.NopCloser(strings.NewReader(string(fileData))),
				}, nil
			default:
				t.Fatalf("unexpected request URL: %s", req.URL.String())
				return nil, nil
			}
		})},
	}

	attachments, _ := adapter.processAttachments(context.Background(), map[string]any{
		"attachments": []any{
			map[string]any{
				"url":          "https://example.com/image",
				"filename":     "screen.png",
				"content_type": "image/png",
			},
			map[string]any{
				"url":          "https://example.com/file",
				"filename":     "doc.pdf",
				"content_type": "application/pdf",
			},
		},
	})
	if len(attachments) != 2 {
		t.Fatalf("expected 2 attachments, got %#v", attachments)
	}
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.Path) == "" {
			t.Fatalf("expected cached local path, got %#v", attachment)
		}
		data, err := os.ReadFile(attachment.Path)
		if err != nil {
			t.Fatalf("expected cached file to exist: %v", err)
		}
		switch attachment.Kind {
		case AttachmentImage:
			if attachment.URL == "" || attachment.DataBase64 == "" {
				t.Fatalf("expected image attachment to preserve url and base64, got %#v", attachment)
			}
			if string(data) != string(pngData) {
				t.Fatalf("unexpected cached image data")
			}
		case AttachmentFile:
			if attachment.MIME != "application/pdf" {
				t.Fatalf("unexpected file MIME: %#v", attachment)
			}
			if string(data) != string(fileData) {
				t.Fatalf("unexpected cached file data")
			}
		default:
			t.Fatalf("unexpected attachment kind: %#v", attachment)
		}
		_ = os.Remove(attachment.Path)
	}
}

func TestSendUsesLatestInboundMessageIDAsReplyTo(t *testing.T) {
	var posts []string
	adapter := &qqAdapter{
		name:           "hermes",
		connected:      true,
		token:          "token-123",
		tokenExpiresAt: time.Now().Add(time.Hour),
		ws:             &websocket.Conn{},
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(req.Body)
			posts = append(posts, req.URL.Path+" "+string(body))
			return jsonResponse(`{}`), nil
		})},
		chatTypes: map[string]string{"group-1": "group"},
		seen:      map[string]time.Time{},
	}

	err := adapter.Send(context.Background(), ChannelBinding{ChannelID: "group-1", LastInboundMessageID: "msg-42", LastInboundAt: time.Now()}, OutboundEvent{
		Kind: OutboundEventText,
		Text: "hello",
	})
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected one post, got %d", len(posts))
	}
	if !strings.Contains(posts[0], "/v2/groups/group-1/messages") || !strings.Contains(posts[0], "\"msg_id\":\"msg-42\"") || !strings.Contains(posts[0], "\"msg_seq\":1") {
		t.Fatalf("expected passive reply body, got %s", posts[0])
	}
}

func TestSendFallsBackToPlainTextWhenMarkdownRejected(t *testing.T) {
	var posts []string
	adapter := &qqAdapter{
		name:            "hermes",
		connected:       true,
		token:           "token-123",
		tokenExpiresAt:  time.Now().Add(time.Hour),
		ws:              &websocket.Conn{},
		markdownSupport: true,
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(req.Body)
			payload := string(body)
			posts = append(posts, req.URL.Path+" "+payload)
			if strings.Contains(payload, `"msg_type":2`) {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"message":"invalid request","code":11255,"err_code":11255}`)),
				}, nil
			}
			return jsonResponse(`{}`), nil
		})},
		chatTypes: map[string]string{"group-1": "group"},
		seen:      map[string]time.Time{},
	}

	err := adapter.Send(context.Background(), ChannelBinding{ChannelID: "group-1", LastInboundMessageID: "msg-42", LastInboundAt: time.Now()}, OutboundEvent{
		Kind: OutboundEventText,
		Text: "hello",
	})
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if len(posts) != 2 {
		t.Fatalf("expected markdown attempt plus text fallback, got %d", len(posts))
	}
	if !strings.Contains(posts[0], `"msg_type":2`) {
		t.Fatalf("expected first request to use markdown, got %s", posts[0])
	}
	if !strings.Contains(posts[0], `"msg_seq":1`) || !strings.Contains(posts[1], `"msg_seq":1`) {
		t.Fatalf("expected markdown fallback to reuse msg_seq=1, got %q and %q", posts[0], posts[1])
	}
	if !strings.Contains(posts[1], `"msg_type":0`) || !strings.Contains(posts[1], `"content":"hello"`) {
		t.Fatalf("expected second request to fallback to text, got %s", posts[1])
	}
}

func TestSendAllowsOutboundWithoutRecentInboundMessage(t *testing.T) {
	var posts []string
	adapter := &qqAdapter{
		name:           "hermes",
		connected:      true,
		token:          "token-123",
		tokenExpiresAt: time.Now().Add(time.Hour),
		ws:             &websocket.Conn{},
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(req.Body)
			posts = append(posts, req.URL.Path+" "+string(body))
			return jsonResponse(`{}`), nil
		})},
		chatTypes: map[string]string{"guild-1": "guild"},
		seen:      map[string]time.Time{},
	}

	err := adapter.Send(context.Background(), ChannelBinding{ChannelID: "guild-1"}, OutboundEvent{
		Kind: OutboundEventText,
		Text: "hello",
	})
	if err != nil {
		t.Fatalf("expected outbound without reply window to succeed, got %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected one outbound request, got %d", len(posts))
	}
	if strings.Contains(posts[0], `"msg_id":`) || strings.Contains(posts[0], `"msg_seq":`) {
		t.Fatalf("expected outbound without inbound message to omit msg_id/msg_seq, got %s", posts[0])
	}
}

func TestSendUsesPersistedReplySequenceForSameInboundMessage(t *testing.T) {
	var posts []string
	store := NewMemoryBindingStore()
	if err := store.Save(ChannelBinding{
		Workspace:             "/tmp/project",
		Platform:              PlatformQQ,
		Adapter:               "hermes",
		TargetID:              "ops",
		ChannelID:             "group-1",
		LastInboundMessageID:  "msg-42",
		LastInboundAt:         time.Now().Add(-61 * time.Minute),
		PassiveReplyCount:     1,
		PassiveReplyStartedAt: time.Now().Add(-30 * time.Minute),
	}); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	mgr := NewManager()
	if err := mgr.SetBindingStore(store); err != nil {
		t.Fatalf("SetBindingStore returned error: %v", err)
	}
	mgr.BindSession(SessionBinding{SessionID: "session-1", Workspace: "/tmp/project"})
	adapter := &qqAdapter{
		name:           "hermes",
		manager:        mgr,
		connected:      true,
		token:          "token-123",
		tokenExpiresAt: time.Now().Add(time.Hour),
		ws:             &websocket.Conn{},
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(req.Body)
			posts = append(posts, req.URL.Path+" "+string(body))
			return jsonResponse(`{}`), nil
		})},
		chatTypes: map[string]string{"group-1": "group"},
		seen:      map[string]time.Time{},
	}

	err := adapter.Send(context.Background(), ChannelBinding{
		Workspace:             "/tmp/project",
		ChannelID:             "group-1",
		LastInboundMessageID:  "msg-42",
		LastInboundAt:         time.Now().Add(-61 * time.Minute),
		PassiveReplyCount:     1,
		PassiveReplyStartedAt: time.Now().Add(-30 * time.Minute),
	}, OutboundEvent{
		Kind: OutboundEventText,
		Text: "hello",
	})
	if err != nil {
		t.Fatalf("expected persisted msg_id to remain usable locally, got %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected persisted msg_id send to proceed, got %d requests", len(posts))
	}
	if !strings.Contains(posts[0], `"msg_id":"msg-42"`) || !strings.Contains(posts[0], `"msg_seq":2`) {
		t.Fatalf("expected persisted reply to continue at msg_seq=2, got %s", posts[0])
	}
	storedList, err := store.ListByWorkspace("/tmp/project")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(storedList) == 0 || storedList[0].PassiveReplyCount != 2 {
		t.Fatalf("expected reply sequence count to persist as 2, got %#v", storedList)
	}
}

func TestSendRefreshesExpiredServerTokenAndRetries(t *testing.T) {
	var messageCalls int
	adapter := &qqAdapter{
		name:           "hermes",
		connected:      true,
		token:          "stale-token",
		tokenExpiresAt: time.Now().Add(time.Hour),
		ws:             &websocket.Conn{},
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.String() {
			case qqTokenURL:
				return jsonResponse(`{"access_token":"fresh-token","expires_in":"3600"}`), nil
			case qqAPIBase + "/v2/groups/group-1/messages":
				messageCalls++
				if got := req.Header.Get("Authorization"); messageCalls == 1 {
					if got != "QQBot stale-token" {
						t.Fatalf("first message call used %q", got)
					}
					return &http.Response{
						StatusCode: http.StatusInternalServerError,
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader(`{"message":"token not exist or expire","code":11244,"err_code":11244}`)),
					}, nil
				}
				if got := req.Header.Get("Authorization"); got != "QQBot fresh-token" {
					t.Fatalf("retry message call used %q", got)
				}
				return jsonResponse(`{}`), nil
			default:
				t.Fatalf("unexpected request URL: %s", req.URL.String())
				return nil, nil
			}
		})},
		chatTypes: map[string]string{"group-1": "group"},
		seen:      map[string]time.Time{},
	}

	err := adapter.Send(context.Background(), ChannelBinding{
		ChannelID:             "group-1",
		LastInboundMessageID:  "msg-42",
		LastInboundAt:         time.Now(),
		PassiveReplyStartedAt: time.Now().Add(-time.Minute),
	}, OutboundEvent{
		Kind: OutboundEventText,
		Text: "hello",
	})
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if messageCalls != 2 {
		t.Fatalf("expected two message calls, got %d", messageCalls)
	}
	if adapter.accessToken() != "fresh-token" {
		t.Fatalf("expected refreshed token cache, got %q", adapter.accessToken())
	}
}

func TestSendPreservesReplyMetadataWhenQQReturns11255(t *testing.T) {
	adapter := &qqAdapter{
		name:           "hermes",
		connected:      true,
		token:          "token-123",
		tokenExpiresAt: time.Now().Add(time.Hour),
		ws:             &websocket.Conn{},
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(req.Body)
			payload := string(body)
			if strings.Contains(payload, `"msg_id":"msg-42"`) {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"message":"invalid request","code":11255,"err_code":11255}`)),
				}, nil
			}
			return jsonResponse(`{}`), nil
		})},
		chatTypes: map[string]string{"group-1": "group"},
		seen:      map[string]time.Time{},
	}

	err := adapter.Send(context.Background(), ChannelBinding{
		ChannelID:             "group-1",
		LastInboundMessageID:  "msg-42",
		LastInboundAt:         time.Now(),
		PassiveReplyCount:     1,
		PassiveReplyStartedAt: time.Now().Add(-time.Minute),
	}, OutboundEvent{
		Kind: OutboundEventText,
		Text: "hello",
	})
	if err == nil || !strings.Contains(err.Error(), `"code":11255`) {
		t.Fatalf("expected passive reply rejection, got %v", err)
	}
}

func TestSendIgnoresLocalPassiveReplyLimit(t *testing.T) {
	var posts []string
	adapter := &qqAdapter{
		name:           "hermes",
		connected:      true,
		token:          "token-123",
		tokenExpiresAt: time.Now().Add(time.Hour),
		ws:             &websocket.Conn{},
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(req.Body)
			posts = append(posts, req.URL.Path+" "+string(body))
			return jsonResponse(`{}`), nil
		})},
		chatTypes: map[string]string{"group-1": "group"},
		seen:      map[string]time.Time{},
	}

	err := adapter.Send(context.Background(), ChannelBinding{
		ChannelID:             "group-1",
		LastInboundMessageID:  "msg-42",
		LastInboundAt:         time.Now(),
		PassiveReplyCount:     999,
		PassiveReplyStartedAt: time.Now().Add(-10 * time.Minute),
	}, OutboundEvent{
		Kind: OutboundEventText,
		Text: "hello",
	})
	if err != nil {
		t.Fatalf("expected send to ignore local passive limit, got %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("expected one outbound request, got %d", len(posts))
	}
	if !strings.Contains(posts[0], `"msg_seq":1000`) {
		t.Fatalf("expected msg_seq to advance from persisted count, got %s", posts[0])
	}
}

func TestSendDoesNotEnforceLocalPassiveReplyCountCap(t *testing.T) {
	var posts []string
	adapter := &qqAdapter{
		name:           "hermes",
		connected:      true,
		token:          "token-123",
		tokenExpiresAt: time.Now().Add(time.Hour),
		ws:             &websocket.Conn{},
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(req.Body)
			posts = append(posts, req.URL.Path+" "+string(body))
			return jsonResponse(`{}`), nil
		})},
		chatTypes: map[string]string{"group-1": "group"},
		seen:      map[string]time.Time{},
	}

	for range 6 {
		err := adapter.Send(context.Background(), ChannelBinding{ChannelID: "group-1", LastInboundMessageID: "msg-42", LastInboundAt: time.Now()}, OutboundEvent{
			Kind: OutboundEventText,
			Text: "hello",
		})
		if err != nil {
			t.Fatalf("Send returned error: %v", err)
		}
	}

	if len(posts) != 6 {
		t.Fatalf("expected repeated sends to remain allowed locally, got %d", len(posts))
	}
}

func TestOutboundTextPreservesStatusForQQ(t *testing.T) {
	adapter := &qqAdapter{}

	if got := adapter.outboundText(OutboundEvent{Kind: OutboundEventStatus, Status: "子任务「review」：正在检查 git status..."}); got != "子任务「review」：正在检查 git status..." {
		t.Fatalf("expected status text to pass through unchanged, got %q", got)
	}
}

func TestFormatOutboundContentPassesThroughUnchanged(t *testing.T) {
	adapter := &qqAdapter{}

	got := adapter.formatOutboundContent("我可以帮你：\n-📝 编写和修改代码-🔍搜索和分析代码库-🐛调试和修复问题")
	want := "我可以帮你：\n-📝 编写和修改代码-🔍搜索和分析代码库-🐛调试和修复问题"
	if got != want {
		t.Fatalf("unexpected formatted content:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestResolveQQCredentialsIgnoresEnvironmentVariables(t *testing.T) {
	t.Setenv("QQ_APP_ID", "shared-app")
	t.Setenv("QQ_CLIENT_SECRET", "shared-secret")
	t.Setenv("GGCODE_QQ_APP_ID", "ggcode-app")
	t.Setenv("GGCODE_QQ_CLIENT_SECRET", "ggcode-secret")

	appID, secret, source := resolveQQCredentials(config.IMAdapterConfig{})
	if appID != "" || secret != "" || source != "unconfigured" {
		t.Fatalf("unexpected credentials resolution: app=%q secret=%q source=%q", appID, secret, source)
	}
}

func TestResolveQQCredentialsUsesConfigOnly(t *testing.T) {
	t.Setenv("QQ_APP_ID", "shared-app")
	t.Setenv("QQ_CLIENT_SECRET", "shared-secret")
	t.Setenv("GGCODE_QQ_APP_ID", "ggcode-app")
	t.Setenv("GGCODE_QQ_CLIENT_SECRET", "ggcode-secret")

	appID, secret, source := resolveQQCredentials(config.IMAdapterConfig{
		Extra: map[string]any{
			"appid":     "config-app",
			"appsecret": "config-secret",
		},
	})
	if appID != "config-app" || secret != "config-secret" || source != "config" {
		t.Fatalf("unexpected credentials resolution: app=%q secret=%q source=%q", appID, secret, source)
	}
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
