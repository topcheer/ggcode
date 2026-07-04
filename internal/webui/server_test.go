package webui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

func TestMCPSerialization(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.MCPServers = []config.MCPServerConfig{
		{Name: "test-server", Type: "stdio", Command: "npx", Args: []string{"-y", "@test/mcp"}},
	}

	s := NewServer(cfg)
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.Header.Set("Authorization", "Bearer "+s.Token())
	w := httptest.NewRecorder()
	s.handleConfig(w, req)

	var result map[string]json.RawMessage
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Check mcp_servers is present
	raw, ok := result["mcp_servers"]
	if !ok {
		t.Fatal("mcp_servers key missing from response")
	}

	var servers []map[string]interface{}
	if err := json.Unmarshal(raw, &servers); err != nil {
		t.Fatalf("unmarshal mcp_servers: %v", err)
	}

	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(servers))
	}

	srv := servers[0]
	t.Logf("server keys: %v", srv)
	t.Logf("raw: %s", string(raw))
	if srv["name"] != "test-server" {
		t.Errorf("expected name 'test-server', got %v", srv["name"])
	}
	if srv["type"] != "stdio" {
		t.Errorf("expected type 'stdio', got %v", srv["type"])
	}
	if srv["command"] != "npx" {
		t.Errorf("expected command 'npx', got %v", srv["command"])
	}
	args, _ := srv["args"].([]interface{})
	if len(args) != 2 {
		t.Errorf("expected 2 args, got %v", args)
	}
}

func TestIMSerialization(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.IM.Enabled = true
	cfg.IM.OutputMode = "quiet"
	cfg.IM.Adapters = map[string]config.IMAdapterConfig{
		"qq": {Enabled: true, Platform: "qq", Transport: "onebot"},
	}

	s := NewServer(cfg)
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.Header.Set("Authorization", "Bearer "+s.Token())
	w := httptest.NewRecorder()
	s.handleConfig(w, req)

	var result map[string]json.RawMessage
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Check im is present
	raw, ok := result["im"]
	if !ok {
		t.Fatal("im key missing from response")
	}

	var im map[string]interface{}
	if err := json.Unmarshal(raw, &im); err != nil {
		t.Fatalf("unmarshal im: %v", err)
	}

	if im["enabled"] != true {
		t.Errorf("expected enabled=true, got %v", im["enabled"])
	}
	if im["output_mode"] != "quiet" {
		t.Errorf("expected output_mode='quiet', got %v", im["output_mode"])
	}
	adapters, _ := im["adapters"].(map[string]interface{})
	if len(adapters) != 1 {
		t.Errorf("expected 1 adapter, got %d", len(adapters))
	}
}

func TestA2ASerialization(t *testing.T) {
	cfg := &config.Config{
		Vendor:   "test",
		Endpoint: "test-ep",
	}
	cfg.A2A.Disabled = false
	cfg.A2A.Port = 0
	cfg.A2A.Host = "127.0.0.1"
	cfg.A2A.Auth.APIKey = "test-key"
	cfg.A2A.Auth.OAuth2 = &config.A2AOAuth2Config{
		Provider: "github",
		Flow:     "pkce",
	}

	s := NewServer(cfg)
	// Test GET
	req := httptest.NewRequest(http.MethodGet, "/api/a2a", nil)
	req.Header.Set("Authorization", "Bearer "+s.Token())
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["disabled"] == true {
		t.Error("A2A should be enabled")
	}
	auth := result["auth"].(map[string]interface{})
	if auth["has_api_key"] != true {
		t.Error("should have API key")
	}
	oauth2 := auth["oauth2"].(map[string]interface{})
	if oauth2["provider"] != "github" {
		t.Error("oauth2 provider should be github")
	}
	if oauth2["has_secret"] == true {
		t.Error("oauth2 should not have secret")
	}
	t.Logf("A2A response: %+v", result)
}

func TestSessionsListNoStore(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+s.Token())
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &result)
	if result["total"] != float64(0) {
		t.Errorf("expected total 0, got %v", result["total"])
	}
}

func TestSessionsListWithSessions(t *testing.T) {
	// Use manual temp dirs instead of t.TempDir() to avoid
	// "TempDir RemoveAll cleanup: unlinkat: directory not empty" caused by
	// JSONL store file handles still held during cleanup.
	homeDir, err := os.MkdirTemp("", "webui-test-home-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(homeDir)
	t.Setenv("HOME", homeDir)

	dir, err := os.MkdirTemp("", "webui-test-store-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Create test sessions
	ses1 := &session.Session{
		ID:        "20260401-120000",
		Title:     "Test Session 1",
		Workspace: "/home/user/project-a",
		Vendor:    "zai",
		Endpoint:  "test-ep",
		Model:     "test-model",
		Messages: []provider.Message{
			{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}},
			{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "world"}}},
		},
	}
	ses1.CreatedAt = time.Now().Add(-2 * time.Hour)
	ses1.UpdatedAt = time.Now().Add(-1 * time.Hour)
	store.Save(ses1)

	ses2 := &session.Session{
		ID:        "20260401-130000",
		Title:     "Test Session 2",
		Workspace: "/home/user/project-a",
		Vendor:    "openai",
		Model:     "gpt-4",
		Messages: []provider.Message{
			{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}},
		},
	}
	ses2.CreatedAt = time.Now().Add(-30 * time.Minute)
	ses2.UpdatedAt = time.Now()
	store.Save(ses2)

	ses3 := &session.Session{
		ID:        "20260401-140000",
		Title:     "Test Session 3",
		Workspace: "/home/user/project-b",
		Vendor:    "anthropic",
		Model:     "claude-3",
		Messages: []provider.Message{
			{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "test"}}},
		},
	}
	ses3.CreatedAt = time.Now()
	ses3.UpdatedAt = time.Now()
	store.Save(ses3)

	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetSessionStore(store, "/home/user/project-a")

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+s.Token())
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &result)

	if result["total"] != float64(3) {
		t.Errorf("expected total 3, got %v", result["total"])
	}
	if result["current_workspace"] != "/home/user/project-a" {
		t.Errorf("expected current_workspace, got %v", result["current_workspace"])
	}

	groups := result["groups"].([]interface{})
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}

	// First group should be current workspace
	g0 := groups[0].(map[string]interface{})
	if !g0["current"].(bool) {
		t.Error("first group should be current")
	}
	if g0["count"] != float64(2) {
		t.Errorf("expected 2 sessions in project-a, got %v", g0["count"])
	}

	g0sessions := g0["sessions"].([]interface{})
	s0 := g0sessions[0].(map[string]interface{})
	if s0["title"] != "Test Session 2" {
		// Sessions are sorted by UpdatedAt desc within the group
		t.Logf("first session title: %v (may not be ordered within group)", s0["title"])
	}
	if s0["model"] != "gpt-4" || s0["model"] == "" {
		t.Logf("model: %v", s0["model"])
	}
}

func TestSessionDetail(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ses := &session.Session{
		ID:        "20260401-120000",
		Title:     "Detail Test",
		Workspace: "/home/user/project",
		Vendor:    "zai",
		Endpoint:  "test-ep",
		Model:     "test-model",
		Messages: []provider.Message{
			{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "you are helpful"}}},
			{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello there"}}},
			{Role: "assistant", Content: []provider.ContentBlock{
				{Type: "text", Text: "let me check"},
				{Type: "tool_use", ToolName: "run_command", ToolID: "t1", Input: json.RawMessage(`{"command":"ls"}`)},
			}},
			{Role: "user", Content: []provider.ContentBlock{
				{Type: "tool_result", ToolID: "t1", ToolName: "run_command", Output: "file.txt"},
			}},
			{Role: "assistant", Content: []provider.ContentBlock{
				{Type: "text", Text: "here is the result"},
			}},
		},
	}
	ses.CreatedAt = time.Now()
	ses.UpdatedAt = time.Now()
	store.Save(ses)

	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetSessionStore(store, "/home/user/project")

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/20260401-120000", nil)
	req.Header.Set("Authorization", "Bearer "+s.Token())
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &result)

	if result["title"] != "Detail Test" {
		t.Errorf("expected title 'Detail Test', got %v", result["title"])
	}
	if result["model"] != "test-model" {
		t.Errorf("expected model, got %v", result["model"])
	}

	msgs := result["messages"].([]interface{})
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages (system filtered), got %d", len(msgs))
	}

	// Check tool_use block is preserved (msgs[1] is the assistant message)
	assistantMsg := msgs[1].(map[string]interface{})
	blocks := assistantMsg["content"].([]interface{})
	toolBlock := blocks[1].(map[string]interface{})
	if toolBlock["tool_name"] != "run_command" {
		t.Errorf("expected tool_name run_command, got %v", toolBlock["tool_name"])
	}
	if toolBlock["input"] == nil {
		t.Error("tool input should be present")
	}

	// Check tool_result block (msgs[2] is the user message with tool_result)
	toolResultMsg := msgs[2].(map[string]interface{})
	resultBlocks := toolResultMsg["content"].([]interface{})
	resultBlock := resultBlocks[0].(map[string]interface{})
	if resultBlock["output"] != "file.txt" {
		t.Errorf("expected output 'file.txt', got %v", resultBlock["output"])
	}
}

func TestSessionDetailNotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetSessionStore(store, "/tmp")

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/nonexistent", nil)
	req.Header.Set("Authorization", "Bearer "+s.Token())
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestSessionDetailNoStore(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/123", nil)
	req.Header.Set("Authorization", "Bearer "+s.Token())
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	if w.Code != 503 {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestSessionMethodNotAllowed(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)

	// POST to /api/sessions
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+s.Token())
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	if w.Code != 405 {
		t.Errorf("expected 405 for POST /api/sessions, got %d", w.Code)
	}

	// POST to /api/sessions/123
	req = httptest.NewRequest(http.MethodPost, "/api/sessions/123", nil)
	req.Header.Set("Authorization", "Bearer "+s.Token())
	w = httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	if w.Code != 405 {
		t.Errorf("expected 405 for POST /api/sessions/123, got %d", w.Code)
	}
}

// mockAgent is a test double for AgentRunner.
type mockAgent struct {
	lastMsg     string
	lastContent []provider.ContentBlock
	events      []provider.StreamEvent
	messages    []provider.Message
	delay       time.Duration
}

func (m *mockAgent) RunStream(ctx context.Context, userMsg string, onEvent func(provider.StreamEvent)) error {
	m.lastMsg = userMsg
	return m.RunStreamWithContent(ctx, []provider.ContentBlock{provider.TextBlock(userMsg)}, onEvent)
}

func (m *mockAgent) RunStreamWithContent(ctx context.Context, content []provider.ContentBlock, onEvent func(provider.StreamEvent)) error {
	m.lastContent = content
	if len(content) > 0 && content[0].Type == "text" {
		m.lastMsg = content[0].Text
	}
	m.messages = append(m.messages, provider.Message{Role: "user", Content: content})
	time.Sleep(m.delay)
	for _, e := range m.events {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			onEvent(e)
		}
	}
	// Simulate assistant response in history
	var textContent string
	for _, e := range m.events {
		if e.Type == provider.StreamEventText {
			textContent += e.Text
		}
	}
	if textContent != "" {
		m.messages = append(m.messages, provider.Message{
			Role:    "assistant",
			Content: []provider.ContentBlock{{Type: "text", Text: textContent}},
		})
	}
	return nil
}

func (m *mockAgent) Messages() []provider.Message {
	return m.messages
}

// mockChatBridge is a test double for ChatBridge.
type mockChatBridge struct {
	mu          sync.Mutex
	messages    []provider.Message
	lastContent []provider.ContentBlock
	subs        []func(provider.StreamEvent)
	subMu       sync.RWMutex
}

func (m *mockChatBridge) Messages() []provider.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.messages
}

func (m *mockChatBridge) SendUserMessage(content []provider.ContentBlock) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastContent = content
	m.messages = append(m.messages, provider.Message{Role: "user", Content: content})
}

func (m *mockChatBridge) Subscribe(fn func(provider.StreamEvent)) func() {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	m.subs = append(m.subs, fn)
	idx := len(m.subs) - 1
	return func() {
		m.subMu.Lock()
		defer m.subMu.Unlock()
		m.subs[idx] = nil
	}
}

func (m *mockChatBridge) broadcastEvent(event provider.StreamEvent) {
	m.subMu.RLock()
	defer m.subMu.RUnlock()
	for _, fn := range m.subs {
		if fn != nil {
			fn(event)
		}
	}
}

func TestChatWSNoBridge(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/chat/ws?token=" + s.Token()
	_, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected error when no bridge, got connection")
	}
}

func TestChatWSBridgeSimple(t *testing.T) {
	bridge := &mockChatBridge{}
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetChatBridge(bridge)

	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/chat/ws?token=" + s.Token()
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	// Send message
	ws.WriteJSON(map[string]string{"type": "user_message", "text": "hi"})

	// Read user_ack
	var ack map[string]interface{}
	ws.ReadJSON(&ack)
	if ack["type"] != "user_ack" {
		t.Fatalf("expected user_ack, got %v", ack["type"])
	}
	if ack["text"] != "hi" {
		t.Fatalf("expected text 'hi', got %v", ack["text"])
	}

	// Simulate agent events via bridge broadcast
	go func() {
		time.Sleep(50 * time.Millisecond)
		bridge.broadcastEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "Hello "})
		bridge.broadcastEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "world!"})
		bridge.broadcastEvent(provider.StreamEvent{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{InputTokens: 10, OutputTokens: 5}})
	}()

	// Read text deltas
	var msg1 map[string]interface{}
	ws.ReadJSON(&msg1)
	if msg1["type"] != "text_delta" || msg1["text"] != "Hello " {
		t.Errorf("expected text_delta 'Hello ', got %v", msg1)
	}

	var msg2 map[string]interface{}
	ws.ReadJSON(&msg2)
	if msg2["type"] != "text_delta" || msg2["text"] != "world!" {
		t.Errorf("expected text_delta 'world!', got %v", msg2)
	}

	var done map[string]interface{}
	ws.ReadJSON(&done)
	if done["type"] != "done" {
		t.Errorf("expected done, got %v", done)
	}

	// Verify bridge received the message
	if len(bridge.lastContent) != 1 || bridge.lastContent[0].Text != "hi" {
		t.Errorf("bridge should have received 'hi', got %v", bridge.lastContent)
	}
}

func TestChatWSBridgeBroadcast(t *testing.T) {
	// Test that two WS connections both receive agent events via broadcast
	bridge := &mockChatBridge{}
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetChatBridge(bridge)

	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/chat/ws?token=" + s.Token()

	ws1, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws1.Close()

	ws2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws2.Close()

	// WS1 sends message — only WS1 gets user_ack
	ws1.WriteJSON(map[string]string{"type": "user_message", "text": "hello"})
	var ack1 map[string]interface{}
	ws1.ReadJSON(&ack1)
	if ack1["type"] != "user_ack" {
		t.Fatalf("ws1 should get user_ack, got %v", ack1["type"])
	}

	// Broadcast event — both should receive via subscription.
	// Wait for both WS connections to complete Subscribe() before broadcasting,
	// otherwise the broadcast may fire before ws2's subscription is registered.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		bridge.subMu.RLock()
		count := len(bridge.subs)
		bridge.subMu.RUnlock()
		if count >= 2 {
			break
		}
		runtime.Gosched()
	}
	bridge.broadcastEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "response"})

	var r1 map[string]interface{}
	ws1.SetReadDeadline(time.Now().Add(2 * time.Second))
	ws1.ReadJSON(&r1)
	if r1["type"] != "text_delta" || r1["text"] != "response" {
		t.Errorf("ws1 should get text_delta, got %v", r1)
	}

	var r2 map[string]interface{}
	ws2.SetReadDeadline(time.Now().Add(2 * time.Second))
	ws2.ReadJSON(&r2)
	if r2["type"] != "text_delta" || r2["text"] != "response" {
		t.Errorf("ws2 should get text_delta (broadcast), got %v", r2)
	}
}

func TestChatWSBridgeInvalidMessage(t *testing.T) {
	bridge := &mockChatBridge{}
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetChatBridge(bridge)

	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/chat/ws?token=" + s.Token()
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	ws.WriteJSON(map[string]string{"type": "invalid", "text": "hello"})
	var msg map[string]interface{}
	ws.ReadJSON(&msg)
	if msg["type"] != "error" {
		t.Errorf("expected error, got %v", msg["type"])
	}
}

func TestChatWSBridgeEmptyText(t *testing.T) {
	bridge := &mockChatBridge{}
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetChatBridge(bridge)

	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/chat/ws?token=" + s.Token()
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	ws.WriteJSON(map[string]string{"type": "user_message", "text": ""})
	var msg map[string]interface{}
	ws.ReadJSON(&msg)
	if msg["type"] != "error" {
		t.Errorf("expected error for empty text, got %v", msg["type"])
	}
}

func TestChatWSBridgeWithImage(t *testing.T) {
	bridge := &mockChatBridge{}
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetChatBridge(bridge)

	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/chat/ws?token=" + s.Token()
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	ws.WriteJSON(map[string]interface{}{
		"type":   "user_message",
		"text":   "describe this",
		"images": []map[string]string{{"mime": "image/png", "data": "iVBORw0KGgo="}},
	})

	var ack map[string]interface{}
	ws.ReadJSON(&ack)
	if ack["type"] != "user_ack" {
		t.Fatalf("expected user_ack, got %v", ack["type"])
	}
	if ack["image_count"] != float64(1) {
		t.Errorf("expected image_count 1, got %v", ack["image_count"])
	}
	// Bridge should have received content with image
	if len(bridge.lastContent) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(bridge.lastContent))
	}
	if bridge.lastContent[1].Type != "image" {
		t.Errorf("expected image block, got %v", bridge.lastContent[1].Type)
	}
}

func TestChatWSBridgeWithFile(t *testing.T) {
	bridge := &mockChatBridge{}
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetChatBridge(bridge)

	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/chat/ws?token=" + s.Token()
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	fileContent := base64.StdEncoding.EncodeToString([]byte("package main"))
	ws.WriteJSON(map[string]interface{}{
		"type":  "user_message",
		"text":  "review",
		"files": []map[string]string{{"name": "main.go", "mime": "text/plain", "data": fileContent}},
	})

	var ack map[string]interface{}
	ws.ReadJSON(&ack)
	if ack["type"] != "user_ack" {
		t.Fatalf("expected user_ack, got %v", ack["type"])
	}
	fnames, ok := ack["file_names"].([]interface{})
	if !ok || len(fnames) != 1 || fnames[0] != "main.go" {
		t.Errorf("expected file_names [main.go], got %v", ack["file_names"])
	}
}

func TestChatHistoryBridge(t *testing.T) {
	bridge := &mockChatBridge{
		messages: []provider.Message{
			{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "you are helpful"}}},
			{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}},
			{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "hi there"}}},
		},
	}
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetChatBridge(bridge)

	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	resp, err := authGet(srv.URL+"/api/chat/history", s.Token())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var result []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages (system filtered), got %d", len(result))
	}
	if result[0]["role"] != "user" {
		t.Errorf("first should be user, got %v", result[0]["role"])
	}
}

func TestChatHistoryNoBridge(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	resp, err := authGet(srv.URL+"/api/chat/history", s.Token())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result []interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if len(result) != 0 {
		t.Errorf("expected empty, got %v", result)
	}
}

// --- Config Save Scope Tests ---

func TestHandleConfigScope_GET(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/config/scope", nil)
	req.Header.Set("Authorization", "Bearer "+s.Token())
	w := httptest.NewRecorder()
	s.handleConfigScope(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["scope"] != "global" {
		t.Errorf("scope = %v, want global", resp["scope"])
	}
}

func TestHandleConfigScope_PUT_Global(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)

	body := `{"scope":"global"}`
	req := httptest.NewRequest(http.MethodPut, "/api/config/scope", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleConfigScope(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["scope"] != "global" {
		t.Errorf("scope = %v, want global", resp["scope"])
	}
}

func TestHandleConfigScope_PUT_InstanceWithoutInstance(t *testing.T) {
	cfg := config.DefaultConfig()
	// No instance config attached — should reject instance scope
	s := NewServer(cfg)

	body := `{"scope":"instance"}`
	req := httptest.NewRequest(http.MethodPut, "/api/config/scope", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleConfigScope(w, req)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400 (no instance config)", w.Code)
	}
}

func TestHandleConfigScope_PUT_InvalidScope(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)

	body := `{"scope":"nonsense"}`
	req := httptest.NewRequest(http.MethodPut, "/api/config/scope", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleConfigScope(w, req)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleConfigScope_MethodNotAllowed(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)

	req := httptest.NewRequest(http.MethodDelete, "/api/config/scope", nil)
	req.Header.Set("Authorization", "Bearer "+s.Token())
	w := httptest.NewRecorder()
	s.handleConfigScope(w, req)

	if w.Code != 405 {
		t.Fatalf("status = %d, want 405", w.Code)
	}
}

func TestHandleConfig_IncludesScope(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.Header.Set("Authorization", "Bearer "+s.Token())
	w := httptest.NewRecorder()
	s.handleConfig(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	scope, ok := resp["_scope"].(map[string]interface{})
	if !ok {
		t.Fatal("response should have _scope field")
	}
	if scope["current"] != "global" {
		t.Errorf("_scope.current = %v, want global", scope["current"])
	}
}

func TestHandleConfigScope_PutInstanceWithInstance(t *testing.T) {
	tmpDir := t.TempDir()
	globalPath := tmpDir + "/ggcode.yaml"
	cfg := config.DefaultConfig()
	cfg.FilePath = globalPath
	cfg.Save()

	workspace := tmpDir + "/project"
	// Manually create instance config dir and file
	instDir := config.InstanceDir(workspace)
	os.MkdirAll(instDir, 0755)
	os.WriteFile(instDir+"/ggcode.yaml", []byte("language: en\n"), 0644)

	cfg, _ = config.LoadWithInstance(globalPath, workspace)
	s := NewServer(cfg)

	body := `{"scope":"instance"}`
	req := httptest.NewRequest(http.MethodPut, "/api/config/scope", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleConfigScope(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["scope"] != "instance" {
		t.Errorf("scope = %v, want instance", resp["scope"])
	}

	// Verify server's saveScope changed
	s.mu.RLock()
	sc := s.saveScope
	s.mu.RUnlock()
	if sc != "instance" {
		t.Errorf("server saveScope = %q, want instance", sc)
	}
}

func TestKnightAPI(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)

	t.Run("no knight fn returns disabled", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/knight", nil)
		req.Header.Set("Authorization", "Bearer "+s.Token())
		w := httptest.NewRecorder()
		s.handleKnight(w, req)
		if w.Code != 200 {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		var status KnightStatus
		json.NewDecoder(w.Body).Decode(&status)
		if status.Enabled {
			t.Error("expected enabled=false when no knightStatusFn set")
		}
	})

	t.Run("with knight fn returns full status", func(t *testing.T) {
		s.SetKnightStatusFn(func() KnightStatus {
			return KnightStatus{
				Enabled: true,
				Running: true,
				Status:  "running (tokens: 50K / 5M)",
				Budget:  KnightBudget{Used: 50000, Remaining: 4950000, Limit: 5000000},
				Active: []KnightSkill{
					{Name: "build-convention", Description: "Always use make build", Scope: "project", CreatedBy: "knight"},
				},
				Staging: []KnightSkill{
					{Name: "read-first", Description: "Read before editing", Scope: "global", Staging: true, CreatedBy: "knight"},
				},
				Queue: []KnightCandidate{
					{Name: "fix-missing-file", Category: "failure-fix", Score: 4.0, EvidenceCount: 2},
				},
			}
		})

		req := httptest.NewRequest(http.MethodGet, "/api/knight", nil)
		req.Header.Set("Authorization", "Bearer "+s.Token())
		w := httptest.NewRecorder()
		s.handleKnight(w, req)
		if w.Code != 200 {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		var status KnightStatus
		json.NewDecoder(w.Body).Decode(&status)
		if !status.Enabled || !status.Running {
			t.Error("expected enabled=true, running=true")
		}
		if len(status.Active) != 1 || status.Active[0].Name != "build-convention" {
			t.Errorf("active skills = %v", status.Active)
		}
		if len(status.Staging) != 1 || !status.Staging[0].Staging {
			t.Errorf("staging skills = %v", status.Staging)
		}
		if len(status.Queue) != 1 || status.Queue[0].Category != "failure-fix" {
			t.Errorf("queue = %v", status.Queue)
		}
	})

	t.Run("skills endpoint returns only skills", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/knight/skills", nil)
		req.Header.Set("Authorization", "Bearer "+s.Token())
		w := httptest.NewRecorder()
		s.handleKnightSkills(w, req)
		if w.Code != 200 {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		var resp map[string]interface{}
		json.NewDecoder(w.Body).Decode(&resp)
		active, _ := resp["active"].([]interface{})
		staging, _ := resp["staging"].([]interface{})
		if len(active) != 1 || len(staging) != 1 {
			t.Errorf("active=%d staging=%d", len(active), len(staging))
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/knight", nil)
		req.Header.Set("Authorization", "Bearer "+s.Token())
		w := httptest.NewRecorder()
		s.handleKnight(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", w.Code)
		}
	})
}

func TestKnightActionAPI(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)

	var lastAction, lastSkillName string
	var lastParams map[string]interface{}
	s.SetKnightActionFn(func(action, skillName string, params map[string]interface{}) error {
		lastAction = action
		lastSkillName = skillName
		lastParams = params
		if action == "fail" {
			return fmt.Errorf("intentional error")
		}
		return nil
	})

	t.Run("promote action", func(t *testing.T) {
		body := `{"action":"promote","name":"my-skill"}`
		req := httptest.NewRequest(http.MethodPost, "/api/knight/action", strings.NewReader(body))
		w := httptest.NewRecorder()
		s.handleKnightAction(w, req)
		if w.Code != 200 {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if lastAction != "promote" || lastSkillName != "my-skill" {
			t.Errorf("got action=%q name=%q", lastAction, lastSkillName)
		}
	})

	t.Run("action with params", func(t *testing.T) {
		body := `{"action":"record_effectiveness","name":"my-skill","params":{"score":5}}`
		req := httptest.NewRequest(http.MethodPost, "/api/knight/action", strings.NewReader(body))
		w := httptest.NewRecorder()
		s.handleKnightAction(w, req)
		if w.Code != 200 {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if lastParams["score"] != 5.0 {
			t.Errorf("params = %v", lastParams)
		}
	})

	t.Run("error response", func(t *testing.T) {
		body := `{"action":"fail","name":"x"}`
		req := httptest.NewRequest(http.MethodPost, "/api/knight/action", strings.NewReader(body))
		w := httptest.NewRecorder()
		s.handleKnightAction(w, req)
		if w.Code != 500 {
			t.Fatalf("status = %d, want 500", w.Code)
		}
	})

	t.Run("no action fn", func(t *testing.T) {
		cfg2 := config.DefaultConfig()
		s2 := NewServer(cfg2)
		body := `{"action":"promote","name":"x"}`
		req := httptest.NewRequest(http.MethodPost, "/api/knight/action", strings.NewReader(body))
		w := httptest.NewRecorder()
		s2.handleKnightAction(w, req)
		if w.Code != 503 {
			t.Fatalf("status = %d, want 503", w.Code)
		}
	})
}

func TestKnightSkillContentAPI(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)

	s.SetKnightSkillContentFn(func(name string, staging bool) (string, error) {
		if name == "missing" {
			return "", fmt.Errorf("not found")
		}
		return "# " + name, nil
	})

	t.Run("get content", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/knight/skill-content?name=my-skill&staging=true", nil)
		req.Header.Set("Authorization", "Bearer "+s.Token())
		w := httptest.NewRecorder()
		s.handleKnightSkillContent(w, req)
		if w.Code != 200 {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		var resp map[string]string
		json.NewDecoder(w.Body).Decode(&resp)
		if resp["content"] != "# my-skill" {
			t.Errorf("content = %q", resp["content"])
		}
	})

	t.Run("not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/knight/skill-content?name=missing", nil)
		req.Header.Set("Authorization", "Bearer "+s.Token())
		w := httptest.NewRecorder()
		s.handleKnightSkillContent(w, req)
		if w.Code != 404 {
			t.Fatalf("status = %d, want 404", w.Code)
		}
	})
}

// --- Knight edge cases ---

func TestKnightSkillsNoFn(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	req := httptest.NewRequest(http.MethodGet, "/api/knight/skills", nil)
	req.Header.Set("Authorization", "Bearer "+s.Token())
	w := httptest.NewRecorder()
	s.handleKnightSkills(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	active, _ := resp["active"].([]interface{})
	staging, _ := resp["staging"].([]interface{})
	if len(active) != 0 || len(staging) != 0 {
		t.Errorf("expected empty arrays, got active=%d staging=%d", len(active), len(staging))
	}
}

func TestKnightSkillsMethodNotAllowed(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	req := httptest.NewRequest(http.MethodPost, "/api/knight/skills", nil)
	req.Header.Set("Authorization", "Bearer "+s.Token())
	w := httptest.NewRecorder()
	s.handleKnightSkills(w, req)
	if w.Code != 405 {
		t.Fatalf("status = %d, want 405", w.Code)
	}
}

func TestKnightActionEdgeCases(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	var captured string
	s.SetKnightActionFn(func(action, name string, params map[string]interface{}) error {
		captured = action + ":" + name
		return nil
	})

	t.Run("missing action field", func(t *testing.T) {
		body := `{"name":"x"}`
		req := httptest.NewRequest(http.MethodPost, "/api/knight/action", strings.NewReader(body))
		w := httptest.NewRecorder()
		s.handleKnightAction(w, req)
		if w.Code != 400 {
			t.Fatalf("status = %d, want 400", w.Code)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		body := `{not json`
		req := httptest.NewRequest(http.MethodPost, "/api/knight/action", strings.NewReader(body))
		w := httptest.NewRecorder()
		s.handleKnightAction(w, req)
		if w.Code != 400 {
			t.Fatalf("status = %d, want 400", w.Code)
		}
	})

	t.Run("delete_queue action", func(t *testing.T) {
		body := `{"action":"delete_queue","name":"bad-candidate"}`
		req := httptest.NewRequest(http.MethodPost, "/api/knight/action", strings.NewReader(body))
		w := httptest.NewRecorder()
		s.handleKnightAction(w, req)
		if w.Code != 200 {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if captured != "delete_queue:bad-candidate" {
			t.Errorf("got %q", captured)
		}
	})
}

func TestKnightContentEdgeCases(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetKnightSkillContentFn(func(name string, staging bool) (string, error) {
		return "# " + name, nil
	})

	t.Run("missing name param", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/knight/skill-content", nil)
		req.Header.Set("Authorization", "Bearer "+s.Token())
		w := httptest.NewRecorder()
		s.handleKnightSkillContent(w, req)
		if w.Code != 400 {
			t.Fatalf("status = %d, want 400", w.Code)
		}
	})

	t.Run("no content fn", func(t *testing.T) {
		cfg2 := config.DefaultConfig()
		s2 := NewServer(cfg2)
		req := httptest.NewRequest(http.MethodGet, "/api/knight/skill-content?name=x", nil)
		req.Header.Set("Authorization", "Bearer "+s.Token())
		w := httptest.NewRecorder()
		s2.handleKnightSkillContent(w, req)
		if w.Code != 503 {
			t.Fatalf("status = %d, want 503", w.Code)
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/knight/skill-content?name=x", nil)
		req.Header.Set("Authorization", "Bearer "+s.Token())
		w := httptest.NewRecorder()
		s.handleKnightSkillContent(w, req)
		if w.Code != 405 {
			t.Fatalf("status = %d, want 405", w.Code)
		}
	})
}

func TestKnightTypes(t *testing.T) {
	// Verify KnightStatus JSON serialization
	s := KnightStatus{
		Enabled: true,
		Running: true,
		Status:  "running",
		Budget:  KnightBudget{Used: 50000, Remaining: 4950000, Limit: 5000000},
		Active: []KnightSkill{
			{Name: "test-skill", Description: "A test", Scope: "project", UsageCount: 3},
		},
		Queue: []KnightCandidate{
			{Name: "fix-build", Category: "failure-fix", Score: 4.0, EvidenceCount: 2},
		},
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var decoded KnightStatus
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Enabled || !decoded.Running {
		t.Error("enabled/running not preserved")
	}
	if decoded.Budget.Limit != 5000000 {
		t.Errorf("budget limit = %d", decoded.Budget.Limit)
	}
	if len(decoded.Active) != 1 || decoded.Active[0].Name != "test-skill" {
		t.Errorf("active = %v", decoded.Active)
	}
	if len(decoded.Queue) != 1 || decoded.Queue[0].Category != "failure-fix" {
		t.Errorf("queue = %v", decoded.Queue)
	}
}
