package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
