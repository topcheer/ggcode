//go:build integration_service

package im

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

func dummyWSConn(t *testing.T) *websocket.Conn {
	t.Helper()
	upgrader := websocket.Upgrader{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		select {}
	}))
	defer s.Close()
	dialer := websocket.Dialer{}
	c, _, err := dialer.DialContext(context.Background(), "ws"+strings.TrimPrefix(s.URL, "http")+"/", nil)
	if err != nil {
		t.Fatalf("dummy websocket dial: %v", err)
	}
	return c
}

func loadE2EConfig(t *testing.T) *config.Config {
	t.Helper()
	cfgPath := config.ConfigPath()
	if _, err := os.Stat(cfgPath); err != nil {
		return nil
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Logf("config load error (skipping): %v", err)
		return nil
	}
	return cfg
}

func resolveE2EProvider(t *testing.T, cfg *config.Config) provider.Provider {
	t.Helper()
	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		t.Logf("resolve endpoint error (skipping): %v", err)
		return nil
	}
	if resolved.APIKey == "" {
		t.Log("no API key in config (skipping)")
		return nil
	}
	prov, err := provider.NewProvider(resolved)
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	return prov
}

func findQQAdapterConfig(t *testing.T, cfg *config.Config) (string, config.IMAdapterConfig, bool) {
	t.Helper()
	for name, adapter := range cfg.IM.Adapters {
		if adapter.Platform == "qq" && adapter.Enabled {
			return name, adapter, true
		}
	}
	return "", config.IMAdapterConfig{}, false
}

func TestE2EAgentImageExtraction(t *testing.T) {
	if os.Getenv("GGCODE_E2E") == "" {
		t.Skip("GGCODE_E2E not set, skipping end-to-end test")
	}

	cfg := loadE2EConfig(t)
	if cfg == nil {
		t.Skip("ggcode config not found or incomplete, skipping")
	}

	prov := resolveE2EProvider(t, cfg)
	if prov == nil {
		t.Skip("no valid provider in config, skipping")
	}

	registry := tool.NewRegistry()
	ag := agent.NewAgent(prov, registry, "You are a helpful assistant for testing. Respond concisely.", 5)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var responseText strings.Builder
	err := ag.RunStream(ctx, "Say 'test ok' and nothing else.", func(event provider.StreamEvent) {
		if event.Type == provider.StreamEventText {
			responseText.WriteString(event.Text)
		}
	})
	if err != nil {
		t.Fatalf("agent RunStream error: %v", err)
	}

	output := strings.TrimSpace(responseText.String())
	if output == "" {
		t.Fatal("agent returned empty response")
	}
	t.Logf("Agent response: %q", output)
	t.Log("Agent streaming pipeline OK")
}

func TestE2EImageExtractionFromMarkdown(t *testing.T) {

	tests := []struct {
		name            string
		input           string
		wantImages      int
		wantTextContain string
	}{
		{
			name:            "markdown image",
			input:           "Here is the result:\n\n![screenshot](https://example.com/img.png)\n\nDone.",
			wantImages:      1,
			wantTextContain: "Done",
		},
		{
			name:            "bare image URL",
			input:           "See this: https://example.com/chart.jpg for details.",
			wantImages:      1,
			wantTextContain: "See this:",
		},
		{
			name:       "multiple images",
			input:      "Before: ![before](https://x.com/before.png)\nAfter: https://x.com/after.jpg\nDone!",
			wantImages: 2,
		},
		{
			name:            "no images",
			input:           "Just a plain text response with no images.",
			wantImages:      0,
			wantTextContain: "plain text",
		},
		{
			name:       "data URL image",
			input:      "Embedded: data:image/png;base64,iVBORw0KGgoAAAANSUhEUg== and text after.",
			wantImages: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			images, remaining := ExtractImagesFromText(tt.input)
			if len(images) != tt.wantImages {
				t.Errorf("ExtractImagesFromText got %d images, want %d (images: %+v)", len(images), tt.wantImages, images)
			}
			if tt.wantTextContain != "" && !strings.Contains(remaining, tt.wantTextContain) {
				t.Errorf("remaining text %q does not contain %q", remaining, tt.wantTextContain)
			}
		})
	}
}

func TestE2EQQImageSendPipeline(t *testing.T) {
	if os.Getenv("GGCODE_E2E") == "" {
		t.Skip("GGCODE_E2E not set, skipping end-to-end test")
	}

	cfg := loadE2EConfig(t)
	if cfg == nil {
		t.Skip("ggcode config not found, skipping")
	}

	adapterName, adapterCfg, found := findQQAdapterConfig(t, cfg)
	if !found {
		t.Skip("no enabled QQ adapter in config, skipping")
	}

	adapter, err := newQQAdapter(adapterName, cfg.IM, adapterCfg, nil)
	if err != nil {
		t.Fatalf("create QQ adapter: %v", err)
	}

	if adapter.appID == "" || adapter.appSecret == "" {
		t.Skip("QQ adapter missing appid/appsecret, skipping")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	token, err := adapter.ensureToken(ctx)
	if err != nil {
		t.Fatalf("ensureToken: %v", err)
	}
	if token == "" {
		t.Fatal("ensureToken returned empty token")
	}
	t.Logf("QQ token obtained successfully (adapter=%s)", adapterName)

	gatewayURL, err := adapter.gatewayURL(ctx)
	if err != nil {
		t.Fatalf("gatewayURL: %v", err)
	}
	t.Logf("QQ gateway URL: %s", gatewayURL)
}

func TestE2EQQSendWithImages(t *testing.T) {
	var (
		tokenCalls    int
		uploadCalls   int
		messageCalls  int
		uploadedFiles []string
		lastMsgType   int
	)

	adapter := &qqAdapter{
		name:            "test-e2e",
		appID:           "test-app-id",
		appSecret:       "test-secret",
		httpClient:      &http.Client{Timeout: 10 * time.Second},
		markdownSupport: false,
		defaultChatType: "c2c",
		chatTypes:       map[string]string{"test-user-123": "c2c"},
		seen:            make(map[string]time.Time),
		uploadCache:     make(map[string]qqUploadCacheEntry),
	}

	adapter.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.String() == qqTokenURL:
			tokenCalls++
			return jsonResponse(`{"access_token":"mock-token-123","expires_in":"7200"}`), nil

		case req.URL.Path == "/v2/users/test-user-123/files" && req.Method == http.MethodPost:
			uploadCalls++
			var body map[string]any
			json.NewDecoder(req.Body).Decode(&body)
			uploadedFiles = append(uploadedFiles, fmt.Sprintf("file_%d", len(uploadedFiles)))
			return jsonResponse(fmt.Sprintf(`{"file_info":"uploaded-file-info-%d"}`, uploadCalls)), nil

		case req.URL.Path == "/v2/users/test-user-123/messages" && req.Method == http.MethodPost:
			messageCalls++
			var body map[string]any
			json.NewDecoder(req.Body).Decode(&body)
			if mt, ok := body["msg_type"].(json.Number); ok {
				if v, err := mt.Int64(); err == nil {
					lastMsgType = int(v)
				}
			}
			return jsonResponse(`{}`), nil

		default:
			t.Logf("unexpected request: %s %s", req.Method, req.URL.String())
			return jsonResponse(`{"error":"not found"}`), nil
		}
	})

	adapter.mu.Lock()
	adapter.connected = true
	adapter.ws = dummyWSConn(t)
	adapter.mu.Unlock()

	pngBase64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8/5+hHgAHggJ/PchI7wAAAABJRU5ErkJggg=="

	content := fmt.Sprintf("Here is the result:\n\n![result](data:image/png;base64,%s)\n\nAnalysis complete.", pngBase64)

	binding := ChannelBinding{
		ChannelID:            "test-user-123",
		Adapter:              "test-e2e",
		LastInboundMessageID: "msg-001",
		PassiveReplyCount:    0,
	}

	err := adapter.Send(context.Background(), binding, OutboundEvent{
		Kind: OutboundEventText,
		Text: content,
	})
	if err != nil {
		t.Fatalf("Send error: %v", err)
	}

	if uploadCalls == 0 {
		t.Error("expected at least one upload call")
	}
	if messageCalls < 2 {
		t.Errorf("expected at least 2 message calls (media + text), got %d", messageCalls)
	}
	t.Logf("uploads=%d messages=%d token_calls=%d last_msg_type=%d", uploadCalls, messageCalls, tokenCalls, lastMsgType)
	t.Log("QQ Send pipeline with images: PASS")
}

func TestE2EQQAdapterWithRealConfig(t *testing.T) {
	if os.Getenv("GGCODE_E2E") == "" {
		t.Skip("GGCODE_E2E not set, skipping end-to-end test")
	}

	cfg := loadE2EConfig(t)
	if cfg == nil {
		t.Skip("ggcode config not found, skipping")
	}

	adapterName, adapterCfg, found := findQQAdapterConfig(t, cfg)
	if !found {
		t.Skip("no enabled QQ adapter in config, skipping")
	}

	adapter, err := newQQAdapter(adapterName, cfg.IM, adapterCfg, nil)
	if err != nil {
		t.Fatalf("create QQ adapter: %v", err)
	}

	if adapter.appID == "" || adapter.appSecret == "" {
		t.Skip("QQ adapter missing appid/appsecret, skipping")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	token, err := adapter.ensureToken(ctx)
	if err != nil {
		t.Fatalf("ensureToken: %v", err)
	}
	if token == "" {
		t.Fatal("ensureToken returned empty token")
	}
	t.Logf("QQ token obtained successfully (adapter=%s)", adapterName)

	gatewayURL, err := adapter.gatewayURL(ctx)
	if err != nil {
		t.Fatalf("gatewayURL: %v", err)
	}
	t.Logf("QQ gateway URL: %s", gatewayURL)
}

func TestE2EFullAgentToQQPipeline(t *testing.T) {
	if os.Getenv("GGCODE_E2E") == "" {
		t.Skip("GGCODE_E2E not set, skipping end-to-end test")
	}

	cfg := loadE2EConfig(t)
	if cfg == nil {
		t.Skip("ggcode config not found, skipping")
	}

	prov := resolveE2EProvider(t, cfg)
	if prov == nil {
		t.Skip("no valid provider in config, skipping")
	}

	registry := tool.NewRegistry()
	ag := agent.NewAgent(prov, registry, "You are a helpful assistant. Respond concisely in English.", 3)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var responseText strings.Builder
	err := ag.RunStream(ctx, "Generate a short test response. Include a markdown image like ![test](https://httpbin.org/image/png) in your response.", func(event provider.StreamEvent) {
		if event.Type == provider.StreamEventText {
			responseText.WriteString(event.Text)
		}
	})
	if err != nil {
		t.Fatalf("agent RunStream error: %v", err)
	}

	output := strings.TrimSpace(responseText.String())
	if output == "" {
		t.Fatal("agent returned empty response")
	}
	t.Logf("LLM response (%d chars): %s", len(output), truncateStr(output, 200))

	images, remainingText := ExtractImagesFromText(output)
	t.Logf("Extracted %d image(s), remaining text: %d chars", len(images), len(remainingText))

	for i, img := range images {
		t.Logf("  image[%d]: kind=%s data_len=%d", i, img.Kind, len(img.Data))
	}

	var (
		uploadCalls  int
		messageCalls int
	)

	mockAdapter := &qqAdapter{
		name:            "e2e-mock",
		appID:           "mock-app",
		appSecret:       "mock-secret",
		httpClient:      &http.Client{Timeout: 10 * time.Second},
		markdownSupport: false,
		defaultChatType: "c2c",
		chatTypes:       map[string]string{"e2e-user": "c2c"},
		seen:            make(map[string]time.Time),
		uploadCache:     make(map[string]qqUploadCacheEntry),
	}

	mockAdapter.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.String() == qqTokenURL:
			return jsonResponse(`{"access_token":"mock-e2e-token","expires_in":"7200"}`), nil

		case strings.Contains(req.URL.Path, "/files") && req.Method == http.MethodPost:
			uploadCalls++
			return jsonResponse(fmt.Sprintf(`{"file_info":"e2e-file-%d"}`, uploadCalls)), nil

		case strings.Contains(req.URL.Path, "/messages") && req.Method == http.MethodPost:
			messageCalls++
			return jsonResponse(`{}`), nil

		default:
			return jsonResponse(`{}`), nil
		}
	})

	mockAdapter.mu.Lock()
	mockAdapter.connected = true
	mockAdapter.ws = dummyWSConn(t)
	mockAdapter.mu.Unlock()

	binding := ChannelBinding{
		ChannelID:            "e2e-user",
		Adapter:              "e2e-mock",
		LastInboundMessageID: "e2e-msg-001",
	}

	sendErr := mockAdapter.Send(context.Background(), binding, OutboundEvent{
		Kind: OutboundEventText,
		Text: output,
	})
	if sendErr != nil {
		t.Fatalf("mock Send error: %v", sendErr)
	}

	t.Logf("Mock QQ pipeline: uploads=%d messages=%d", uploadCalls, messageCalls)

	if len(images) > 0 && uploadCalls == 0 {
		t.Errorf("extracted %d images but got 0 upload calls", len(images))
	}

	if messageCalls == 0 {
		t.Error("expected at least one message call")
	}

	t.Log("Full agent→QQ pipeline E2E test: PASS")
}
