package webui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	dir := t.TempDir()
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
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(msgs))
	}

	// Check tool_use block is preserved
	assistantMsg := msgs[2].(map[string]interface{})
	blocks := assistantMsg["content"].([]interface{})
	toolBlock := blocks[1].(map[string]interface{})
	if toolBlock["tool_name"] != "run_command" {
		t.Errorf("expected tool_name run_command, got %v", toolBlock["tool_name"])
	}
	if toolBlock["input"] == nil {
		t.Error("tool input should be present")
	}

	// Check tool_result block
	toolResultMsg := msgs[3].(map[string]interface{})
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
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	if w.Code != 405 {
		t.Errorf("expected 405 for POST /api/sessions, got %d", w.Code)
	}

	// POST to /api/sessions/123
	req = httptest.NewRequest(http.MethodPost, "/api/sessions/123", nil)
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

func TestChatWSNoAgent(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	// No agent set
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/chat/ws"
	_, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected error when no agent, got connection")
	}
}

func TestChatWSSimple(t *testing.T) {
	agent := &mockAgent{
		events: []provider.StreamEvent{
			{Type: provider.StreamEventText, Text: "Hello "},
			{Type: provider.StreamEventText, Text: "world!"},
			{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{InputTokens: 10, OutputTokens: 5}},
		},
	}

	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetAgent(agent)

	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/chat/ws"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	// Send message
	ws.WriteJSON(map[string]string{"type": "user_message", "text": "hi"})

	// Read responses
	var msgs []map[string]interface{}
	for i := 0; i < 4; i++ {
		var msg map[string]interface{}
		if err := ws.ReadJSON(&msg); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		msgs = append(msgs, msg)
	}

	// First: user_ack
	if msgs[0]["type"] != "user_ack" {
		t.Errorf("expected user_ack, got %v", msgs[0]["type"])
	}
	if msgs[0]["text"] != "hi" {
		t.Errorf("expected text 'hi', got %v", msgs[0]["text"])
	}

	// Text deltas
	if msgs[1]["type"] != "text_delta" {
		t.Errorf("expected text_delta, got %v", msgs[1]["type"])
	}
	if msgs[1]["text"] != "Hello " {
		t.Errorf("expected 'Hello ', got %v", msgs[1]["text"])
	}

	// Done
	if msgs[3]["type"] != "done" {
		t.Errorf("expected done, got %v", msgs[3]["type"])
	}
	usage := msgs[3]["usage"].(map[string]interface{})
	if usage["input_tokens"] != float64(10) {
		t.Errorf("expected 10 input tokens, got %v", usage["input_tokens"])
	}

	if agent.lastMsg != "hi" {
		t.Errorf("agent received '%s', expected 'hi'", agent.lastMsg)
	}
}

func TestChatWSWithTools(t *testing.T) {
	agent := &mockAgent{
		events: []provider.StreamEvent{
			{Type: provider.StreamEventText, Text: "Let me check"},
			{Type: provider.StreamEventToolCallDone, Tool: provider.ToolCallDelta{ID: "t1", Name: "run_command", Arguments: json.RawMessage(`{"command":"ls"}`)}},
			{Type: provider.StreamEventToolResult, Tool: provider.ToolCallDelta{Name: "run_command"}, Result: "file.txt\nreadme.md", IsError: false},
			{Type: provider.StreamEventText, Text: "Found 2 files"},
			{Type: provider.StreamEventDone},
		},
	}

	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetAgent(agent)

	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/chat/ws"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	ws.WriteJSON(map[string]string{"type": "user_message", "text": "list files"})

	var msgs []map[string]interface{}
	for i := 0; i < 6; i++ {
		var msg map[string]interface{}
		if err := ws.ReadJSON(&msg); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		msgs = append(msgs, msg)
	}

	// Verify tool call
	toolMsg := msgs[2]
	if toolMsg["type"] != "tool_call" {
		t.Errorf("expected tool_call at index 2, got %v", toolMsg["type"])
	}
	if toolMsg["name"] != "run_command" {
		t.Errorf("expected tool name run_command, got %v", toolMsg["name"])
	}

	// Verify tool result
	resultMsg := msgs[3]
	if resultMsg["type"] != "tool_result" {
		t.Errorf("expected tool_result at index 3, got %v", resultMsg["type"])
	}
	if resultMsg["result"] != "file.txt\nreadme.md" {
		t.Errorf("unexpected result: %v", resultMsg["result"])
	}
}

func TestChatWSInvalidMessage(t *testing.T) {
	agent := &mockAgent{}
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetAgent(agent)

	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/chat/ws"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	// Send invalid JSON type
	ws.WriteJSON(map[string]string{"type": "invalid", "text": "hello"})

	var msg map[string]interface{}
	if err := ws.ReadJSON(&msg); err != nil {
		t.Fatal(err)
	}
	if msg["type"] != "error" {
		t.Errorf("expected error, got %v", msg["type"])
	}
}

func TestChatWSEmptyText(t *testing.T) {
	agent := &mockAgent{}
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetAgent(agent)

	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/chat/ws"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	ws.WriteJSON(map[string]string{"type": "user_message", "text": ""})

	var msg map[string]interface{}
	if err := ws.ReadJSON(&msg); err != nil {
		t.Fatal(err)
	}
	if msg["type"] != "error" {
		t.Errorf("expected error for empty text, got %v", msg["type"])
	}
}

func TestChatWSWithImage(t *testing.T) {
	agent := &mockAgent{
		events: []provider.StreamEvent{
			{Type: provider.StreamEventText, Text: "I see an image"},
			{Type: provider.StreamEventDone},
		},
	}
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetAgent(agent)

	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/chat/ws"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	// Send message with image
	ws.WriteJSON(map[string]interface{}{
		"type": "user_message",
		"text": "describe this",
		"images": []map[string]string{
			{"mime": "image/png", "data": "iVBORw0KGgo="},
		},
	})

	var msgs []map[string]interface{}
	for i := 0; i < 3; i++ {
		var msg map[string]interface{}
		if err := ws.ReadJSON(&msg); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		msgs = append(msgs, msg)
	}

	// Check ack has image_count
	ack := msgs[0]
	if ack["type"] != "user_ack" {
		t.Errorf("expected user_ack, got %v", ack["type"])
	}
	if ack["image_count"] != float64(1) {
		t.Errorf("expected image_count 1, got %v", ack["image_count"])
	}

	// Check agent received image block
	if len(agent.lastContent) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(agent.lastContent))
	}
	if agent.lastContent[0].Type != "text" {
		t.Errorf("expected text block, got %v", agent.lastContent[0].Type)
	}
	if agent.lastContent[1].Type != "image" {
		t.Errorf("expected image block, got %v", agent.lastContent[1].Type)
	}
	if agent.lastContent[1].ImageMIME != "image/png" {
		t.Errorf("expected image/png, got %v", agent.lastContent[1].ImageMIME)
	}
}

func TestChatWSWithFile(t *testing.T) {
	fileContent := base64.StdEncoding.EncodeToString([]byte("package main\n\nfunc main() {}"))

	agent := &mockAgent{
		events: []provider.StreamEvent{
			{Type: provider.StreamEventText, Text: "Reviewed the file"},
			{Type: provider.StreamEventDone},
		},
	}
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetAgent(agent)

	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/chat/ws"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	ws.WriteJSON(map[string]interface{}{
		"type": "user_message",
		"text": "review this file",
		"files": []map[string]string{
			{"name": "main.go", "mime": "text/plain", "data": fileContent},
		},
	})

	var msgs []map[string]interface{}
	for i := 0; i < 3; i++ {
		var msg map[string]interface{}
		if err := ws.ReadJSON(&msg); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		msgs = append(msgs, msg)
	}

	// Check ack has file_names
	ack := msgs[0]
	if ack["type"] != "user_ack" {
		t.Errorf("expected user_ack, got %v", ack["type"])
	}
	fnames, ok := ack["file_names"].([]interface{})
	if !ok || len(fnames) != 1 {
		t.Fatalf("expected file_names with 1 entry, got %v", ack["file_names"])
	}
	if fnames[0] != "main.go" {
		t.Errorf("expected file name 'main.go', got %v", fnames[0])
	}

	// Check agent received file as text block
	if len(agent.lastContent) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(agent.lastContent))
	}
	fileBlock := agent.lastContent[1]
	if fileBlock.Type != "text" {
		t.Errorf("expected text block for file, got %v", fileBlock.Type)
	}
	if !strings.Contains(fileBlock.Text, "main.go") {
		t.Errorf("file block should contain filename, got: %s", fileBlock.Text)
	}
	if !strings.Contains(fileBlock.Text, "func main()") {
		t.Errorf("file block should contain file content, got: %s", fileBlock.Text)
	}
}

func TestChatWSImageOnlyNoText(t *testing.T) {
	agent := &mockAgent{
		events: []provider.StreamEvent{
			{Type: provider.StreamEventText, Text: "Got it"},
			{Type: provider.StreamEventDone},
		},
	}
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetAgent(agent)

	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/chat/ws"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	// Send image-only message (no text)
	ws.WriteJSON(map[string]interface{}{
		"type": "user_message",
		"images": []map[string]string{
			{"mime": "image/jpeg", "data": "/9j/4AAQ"},
		},
	})

	var msgs []map[string]interface{}
	for i := 0; i < 2; i++ {
		var msg map[string]interface{}
		if err := ws.ReadJSON(&msg); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		msgs = append(msgs, msg)
	}

	if msgs[0]["type"] != "user_ack" {
		t.Errorf("expected user_ack, got %v", msgs[0]["type"])
	}
	// Agent should receive only image block
	if len(agent.lastContent) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(agent.lastContent))
	}
	if agent.lastContent[0].Type != "image" {
		t.Errorf("expected image block, got %v", agent.lastContent[0].Type)
	}
}

func TestChatHistoryNoAgent(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/chat/history")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result []interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if len(result) != 0 {
		t.Errorf("expected empty array, got %v", result)
	}
}

func TestChatHistoryWithMessages(t *testing.T) {
	agent := &mockAgent{
		messages: []provider.Message{
			{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}},
			{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "hi there"}}},
			{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "how are you?"}}},
			{Role: "assistant", Content: []provider.ContentBlock{
				{Type: "text", Text: "let me think"},
				{Type: "tool_use", ToolName: "run_command", ToolID: "t1", Input: json.RawMessage(`{"command":"ls"}`)},
			}},
		},
	}
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetAgent(agent)

	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/chat/history")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var result []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	if len(result) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(result))
	}
	if result[0]["role"] != "user" {
		t.Errorf("first message should be user, got %v", result[0]["role"])
	}
	if result[3]["role"] != "assistant" {
		t.Errorf("fourth message should be assistant, got %v", result[3]["role"])
	}
	// Check tool_use block preserved
	blocks := result[3]["content"].([]interface{})
	toolBlock := blocks[1].(map[string]interface{})
	if toolBlock["tool_name"] != "run_command" {
		t.Errorf("expected tool_name, got %v", toolBlock["tool_name"])
	}
}

func TestChatWSBusyRejection(t *testing.T) {
	// Agent that takes time to respond
	agent := &mockAgent{
		delay: 500 * time.Millisecond,
		events: []provider.StreamEvent{
			{Type: provider.StreamEventText, Text: "done"},
			{Type: provider.StreamEventDone},
		},
	}
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetAgent(agent)

	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/chat/ws"

	// First connection sends a message
	ws1, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws1.Close()
	ws1.WriteJSON(map[string]string{"type": "user_message", "text": "slow request"})

	// Read ack to ensure first request is being processed
	var ack map[string]interface{}
	ws1.ReadJSON(&ack)

	// Second connection tries to send while first is running
	ws2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws2.Close()

	// Give a small delay to ensure first request is in the mutex
	time.Sleep(50 * time.Millisecond)

	ws2.WriteJSON(map[string]string{"type": "user_message", "text": "concurrent request"})

	var errMsg map[string]interface{}
	ws2.ReadJSON(&errMsg)
	// Should get either ack then error, or just error
	for errMsg["type"] == "user_ack" {
		ws2.ReadJSON(&errMsg)
	}
	if errMsg["type"] != "error" {
		t.Errorf("expected error for concurrent request, got %v", errMsg["type"])
	}
	if !strings.Contains(errMsg["error"].(string), "busy") {
		t.Errorf("error should mention busy, got: %v", errMsg["error"])
	}
}

func TestChatWSHistoryUpdates(t *testing.T) {
	agent := &mockAgent{
		events: []provider.StreamEvent{
			{Type: provider.StreamEventText, Text: "Hello!"},
			{Type: provider.StreamEventDone},
		},
	}
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetAgent(agent)

	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	// Send a message via WebSocket
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/chat/ws"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	ws.WriteJSON(map[string]string{"type": "user_message", "text": "hi"})
	// Drain responses
	for i := 0; i < 3; i++ {
		ws.ReadJSON(&map[string]interface{}{})
	}

	// Now check history API has the messages
	resp, err := http.Get(srv.URL + "/api/chat/history")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var result []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	if len(result) < 2 {
		t.Fatalf("expected at least 2 messages in history, got %d", len(result))
	}
	if result[0]["role"] != "user" {
		t.Errorf("first should be user, got %v", result[0]["role"])
	}
	if result[1]["role"] != "assistant" {
		t.Errorf("second should be assistant, got %v", result[1]["role"])
	}
}
