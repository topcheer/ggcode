package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestMCPWithRuntime(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.MCPServers = []config.MCPServerConfig{
		{Name: "filesystem", Type: "stdio", Command: "npx", Args: []string{"-y", "@test/mcp"}},
	}

	s := NewServer(cfg)
	s.SetMCPStatusFn(func() map[string]MCPRuntimeStatus {
		return map[string]MCPRuntimeStatus{
			"filesystem": {Connected: true, Tools: []string{"read_file", "write_file"}},
		}
	})

	// Test merged MCP endpoint
	req := httptest.NewRequest(http.MethodGet, "/api/mcp", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	t.Logf("Response: %s", w.Body.String())

	var result []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1, got %d", len(result))
	}
	srv := result[0]
	t.Logf("Server: %v", srv)

	// Check runtime is present
	rt, ok := srv["runtime"]
	if !ok {
		t.Fatal("runtime key missing")
	}
	rtMap, ok := rt.(map[string]interface{})
	if !ok {
		t.Fatalf("runtime is %T, not map", rt)
	}
	if rtMap["connected"] != true {
		t.Errorf("expected connected=true, got %v", rtMap["connected"])
	}

	// Test status-only endpoint
	req2 := httptest.NewRequest(http.MethodGet, "/api/mcp/status", nil)
	w2 := httptest.NewRecorder()
	s.mux.ServeHTTP(w2, req2)
	t.Logf("Status: %s", w2.Body.String())
}

func TestMCPWithoutRuntime(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.MCPServers = []config.MCPServerConfig{
		{Name: "test", Type: "http", URL: "http://localhost:3000"},
	}

	s := NewServer(cfg)
	// No MCPStatusFn set — simulates no runtime

	req := httptest.NewRequest(http.MethodGet, "/api/mcp", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	var result []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1, got %d", len(result))
	}
	srv := result[0]
	t.Logf("Server: %v", srv)

	// runtime should be nil/missing
	if rt, ok := srv["runtime"]; ok && rt != nil {
		t.Errorf("expected no runtime, got %v", rt)
	}
}
