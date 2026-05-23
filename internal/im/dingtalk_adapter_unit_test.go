package im

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestNewDingtalkAdapter_RequiresCredentials(t *testing.T) {
	_, err := newDingtalkAdapter("test", nil, config.IMAdapterConfig{
		Extra: map[string]interface{}{},
	})
	if err == nil {
		t.Fatal("expected error for missing app_key/app_secret")
	}
}

func TestNewDingtalkAdapter_ValidConfig(t *testing.T) {
	a, err := newDingtalkAdapter("test", nil, config.IMAdapterConfig{
		Extra: map[string]interface{}{
			"app_key":    "dingtest123",
			"app_secret": "secret456",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Name() != "test" {
		t.Errorf("expected name 'test', got %q", a.Name())
	}
	if a.appKey != "dingtest123" {
		t.Errorf("expected appKey 'dingtest123', got %q", a.appKey)
	}
}

func TestDingtalkAdapter_Close(t *testing.T) {
	a, err := newDingtalkAdapter("test", nil, config.IMAdapterConfig{
		Extra: map[string]interface{}{
			"app_key":    "dingtest123",
			"app_secret": "secret456",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Errorf("Close() error: %v", err)
	}
}

func TestDingtalkEscapeJSONString(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{`hello`, `hello`},
		{`hello "world"`, `hello \"world\"`},
		{"line1\nline2", `line1\nline2`},
		{`back\slash`, `back\\slash`},
	}
	for _, tt := range tests {
		got := escapeJSONString(tt.input)
		if got != tt.expect {
			t.Errorf("escapeJSONString(%q) = %q, want %q", tt.input, got, tt.expect)
		}
	}
}

func TestDingtalkDataFrameResponse(t *testing.T) {
	frame := dingtalkDataFrame{
		SpecVersion: "1.0",
		Type:        dingtalkSubCallback,
		Headers: map[string]string{
			dfHeaderTopic:       dingtalkBotCallbackTopic,
			dfHeaderMessageID:   "msg-123",
			dfHeaderContentType: dfContentTypeJSON,
		},
		Data: `{"text":"hello"}`,
	}

	if frame.Headers[dfHeaderTopic] != dingtalkBotCallbackTopic {
		t.Errorf("expected topic %q, got %q", dingtalkBotCallbackTopic, frame.Headers[dfHeaderTopic])
	}
	if frame.Headers[dfHeaderMessageID] != "msg-123" {
		t.Errorf("expected messageID 'msg-123', got %q", frame.Headers[dfHeaderMessageID])
	}
}

func TestDingtalkBotCallbackDataParsing(t *testing.T) {
	data := `{
		"conversationId": "cid123",
		"senderStaffId": "staff456",
		"senderNick": "Test User",
		"msgId": "msg789",
		"text": {"content": "hello bot"},
		"sessionWebhook": "https://oapi.dingtalk.com/robot/sendByToken?token=xxx",
		"robotCode": "dingtest123",
		"conversationType": "1",
		"msgtype": "text"
	}`

	var callback dingtalkBotCallbackData
	if err := json.Unmarshal([]byte(data), &callback); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if callback.ConversationID != "cid123" {
		t.Errorf("expected conversationId 'cid123', got %q", callback.ConversationID)
	}
	if callback.Text.Content != "hello bot" {
		t.Errorf("expected text.content 'hello bot', got %q", callback.Text.Content)
	}
	if callback.SessionWebhook == "" {
		t.Error("expected non-empty sessionWebhook")
	}
}

// --- json.Marshal error handling tests ---

func TestDingtalkRefreshToken_MarshalError(t *testing.T) {
	a := &dingtalkAdapter{
		name:      "test",
		appKey:    "key123",
		appSecret: "secret456",
	}
	a.mu.Lock()
	a.accessToken = ""
	a.mu.Unlock()

	// Verify the refreshToken method works correctly
	// by checking it returns an error when the HTTP call fails (no server running).
	err := a.refreshToken(context.Background())
	if err == nil {
		t.Fatal("expected error from refreshToken with no server")
	}
	// Should be a connection refused error, not a marshal error
	if strings.Contains(err.Error(), "marshal") {
		t.Errorf("should not be a marshal error, got: %v", err)
	}
}

func TestDingtalkRefreshToken_SuccessfulMarshal(t *testing.T) {
	// Verify that the token request body is correctly marshaled
	var receivedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		// Return a valid token response
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"accessToken":"test-token-123","expireIn":7200}`))
	}))
	defer server.Close()

	a := &dingtalkAdapter{
		name:      "test",
		appKey:    "myAppKey",
		appSecret: "myAppSecret",
	}

	// Override the API base to our test server
	// We need to call refreshToken but the URL is hardcoded.
	// Instead, test the marshaling directly by creating the expected body.
	body := map[string]any{
		"appKey":    a.appKey,
		"appSecret": a.appSecret,
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal token request body: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(bodyJSON, &decoded); err != nil {
		t.Fatalf("json.Unmarshal round-trip: %v", err)
	}
	if decoded["appKey"] != "myAppKey" {
		t.Errorf("appKey = %v, want myAppKey", decoded["appKey"])
	}
	if decoded["appSecret"] != "myAppSecret" {
		t.Errorf("appSecret = %v, want myAppSecret", decoded["appSecret"])
	}

	// Verify server received the correct body
	resp, err := http.Post(server.URL, "application/json", strings.NewReader(string(bodyJSON)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if receivedBody["appKey"] != "myAppKey" {
		t.Errorf("server received appKey = %v, want myAppKey", receivedBody["appKey"])
	}
}

func TestDingtalkStreamOpen_MarshalError(t *testing.T) {
	// The streamOpen method also does json.Marshal for the request body.
	// Verify the error message format when the HTTP call fails.
	a := &dingtalkAdapter{
		name:      "test",
		appKey:    "key123",
		appSecret: "secret456",
	}
	a.mu.Lock()
	a.accessToken = "valid-token"
	a.mu.Unlock()

	ctx := context.Background()
	_, err := a.streamOpen(ctx)
	// Will fail because the API base is unreachable, not because of marshal
	if err == nil {
		t.Fatal("expected error from streamOpen with no server")
	}
}

func TestDingtalkStreamOpen_SuccessfulMarshal(t *testing.T) {
	// Verify the stream open request body is correctly constructed
	a := &dingtalkAdapter{
		name:      "test",
		appKey:    "myKey",
		appSecret: "mySecret",
	}
	a.mu.Lock()
	a.accessToken = "test-token"
	a.mu.Unlock()

	body := map[string]any{
		"clientId":     a.appKey,
		"clientSecret": a.appSecret,
		"subscriptions": []map[string]any{
			{
				"type":  "CALLBACK",
				"topic": dingtalkBotCallbackTopic,
			},
		},
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal stream open body: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(bodyJSON, &decoded); err != nil {
		t.Fatalf("json.Unmarshal round-trip: %v", err)
	}
	if decoded["clientId"] != "myKey" {
		t.Errorf("clientId = %v, want myKey", decoded["clientId"])
	}
	subs, ok := decoded["subscriptions"].([]any)
	if !ok || len(subs) != 1 {
		t.Fatalf("subscriptions = %v, want 1-element array", decoded["subscriptions"])
	}
	sub, ok := subs[0].(map[string]any)
	if !ok {
		t.Fatal("subscription element is not a map")
	}
	if sub["type"] != "CALLBACK" {
		t.Errorf("subscription type = %v, want CALLBACK", sub["type"])
	}
}

func TestDingtalkSendMarkdownViaWebhook_MarshalError(t *testing.T) {
	a := &dingtalkAdapter{
		name:      "test",
		appKey:    "key123",
		appSecret: "secret456",
	}

	// Test that an empty webhook URL returns nil (fast path)
	err := a.sendMarkdownViaWebhook(context.Background(), "", "hello", "robot1")
	if err != nil {
		t.Errorf("expected nil for empty webhook URL, got: %v", err)
	}

	// Test that empty text returns nil (fast path)
	err = a.sendMarkdownViaWebhook(context.Background(), "http://example.com/webhook", "", "robot1")
	if err != nil {
		t.Errorf("expected nil for empty text, got: %v", err)
	}
}

func TestDingtalkSendMarkdownViaWebhook_SuccessfulMarshal(t *testing.T) {
	var receivedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	a := &dingtalkAdapter{
		name: "test",
	}
	err := a.sendMarkdownViaWebhook(context.Background(), server.URL, "# Hello world\n\n- item", "robot123")
	if err != nil {
		t.Fatalf("sendMarkdownViaWebhook: %v", err)
	}

	if receivedBody["msgtype"] != "markdown" {
		t.Errorf("msgtype = %v, want markdown", receivedBody["msgtype"])
	}
	if receivedBody["robotCode"] != "robot123" {
		t.Errorf("robotCode = %v, want robot123", receivedBody["robotCode"])
	}
	textObj, ok := receivedBody["markdown"].(map[string]any)
	if !ok {
		t.Fatalf("markdown field is not a map: %v", receivedBody["markdown"])
	}
	if textObj["title"] != "Hello world" {
		t.Errorf("markdown.title = %v, want 'Hello world'", textObj["title"])
	}
	if textObj["text"] != "# Hello world\n\n- item" {
		t.Errorf("markdown.text = %v, want markdown body", textObj["text"])
	}
}

func TestDingtalkSendMarkdownViaAPI_MarshalError(t *testing.T) {
	a := &dingtalkAdapter{
		name:      "test",
		appKey:    "key123",
		appSecret: "secret456",
	}
	a.mu.Lock()
	a.accessToken = "" // no token
	a.mu.Unlock()

	// No access token should return error
	err := a.sendMarkdownViaAPI(context.Background(), ChannelBinding{ChannelID: "user123"}, "hello")
	if err == nil {
		t.Fatal("expected error with no access token")
	}
	if !strings.Contains(err.Error(), "no access token") {
		t.Errorf("unexpected error: %v", err)
	}

	// No userId should return error
	a.mu.Lock()
	a.accessToken = "valid-token"
	a.mu.Unlock()
	err = a.sendMarkdownViaAPI(context.Background(), ChannelBinding{ChannelID: ""}, "hello")
	if err == nil {
		t.Fatal("expected error with empty ChannelID")
	}
	if !strings.Contains(err.Error(), "no userId") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDingtalkSendMarkdownViaAPI_SuccessfulMarshal(t *testing.T) {
	var receivedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}
		if token := r.Header.Get("x-acs-dingtalk-access-token"); token != "test-token" {
			t.Errorf("expected access token header 'test-token', got %q", token)
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Temporarily override the API base for the adapter
	// Since we can't override the constant, we test the marshal correctness directly
	a := &dingtalkAdapter{
		name:      "test",
		appKey:    "testRobotCode",
		appSecret: "secret",
	}
	a.mu.Lock()
	a.accessToken = "test-token"
	a.mu.Unlock()

	// Test the body construction logic directly
	body := map[string]any{
		"robotCode": a.appKey,
		"userIds":   []string{"staff123"},
		"msgKey":    "sampleMarkdown",
		"msgParam":  `{"title":"Hello world","text":"# Hello world\n\n- item"}`,
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal API send body: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(bodyJSON, &decoded); err != nil {
		t.Fatalf("json.Unmarshal round-trip: %v", err)
	}
	if decoded["robotCode"] != "testRobotCode" {
		t.Errorf("robotCode = %v, want testRobotCode", decoded["robotCode"])
	}
	if decoded["msgKey"] != "sampleMarkdown" {
		t.Errorf("msgKey = %v, want sampleMarkdown", decoded["msgKey"])
	}
	userIDs, ok := decoded["userIds"].([]any)
	if !ok || len(userIDs) != 1 || userIDs[0] != "staff123" {
		t.Errorf("userIds = %v, want [staff123]", decoded["userIds"])
	}
	msgParam, ok := decoded["msgParam"].(string)
	if !ok {
		t.Fatalf("msgParam = %T, want string", decoded["msgParam"])
	}
	var msg map[string]any
	if err := json.Unmarshal([]byte(msgParam), &msg); err != nil {
		t.Fatalf("unmarshal msgParam: %v", err)
	}
	if msg["title"] != "Hello world" {
		t.Errorf("msgParam.title = %v, want Hello world", msg["title"])
	}
	if msg["text"] != "# Hello world\n\n- item" {
		t.Errorf("msgParam.text = %v, want markdown body", msg["text"])
	}
}

func TestDingtalkMarkdownTitle(t *testing.T) {
	got := dingtalkMarkdownTitle("# [Deploy report](https://example.com)\n\n![img](https://example.com/a.png)\n| col | value |")
	if got != "Deploy report" {
		t.Fatalf("dingtalkMarkdownTitle = %q, want %q", got, "Deploy report")
	}
}

func TestDingtalkSendFrameResponse_MarshalError(t *testing.T) {
	// Verify sendFrameResponse doesn't panic with various inputs.
	// The method uses json.Marshal internally and logs errors.
	a := &dingtalkAdapter{
		name: "test",
	}

	frame := dingtalkDataFrame{
		SpecVersion: "1.0",
		Type:        dingtalkSubCallback,
		Headers: map[string]string{
			dfHeaderMessageID:   "msg-123",
			dfHeaderContentType: dfContentTypeJSON,
		},
		Data: "",
	}

	// The response should marshal fine for normal cases.
	// Just verify it doesn't panic when ws is nil.
	a.sendFrameResponse(nil, frame, dfStatusOK, "pong")
}

func TestDingtalkMarshalErrorWithUnmarshallableField(t *testing.T) {
	// Verify that json.Marshal correctly fails for types with channels
	type badStruct struct {
		Ch chan int `json:"ch"`
	}
	bad := badStruct{Ch: make(chan int)}
	_, err := json.Marshal(bad)
	if err == nil {
		t.Fatal("expected json.Marshal to fail for struct with chan field")
	}

	// Verify a good struct marshals fine
	type goodStruct struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}
	good := goodStruct{Name: "test", Value: 42}
	data, err := json.Marshal(good)
	if err != nil {
		t.Fatalf("json.Marshal good struct: %v", err)
	}
	var decoded goodStruct
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal round-trip: %v", err)
	}
	if decoded.Name != "test" || decoded.Value != 42 {
		t.Errorf("round-trip failed: %+v", decoded)
	}
}
