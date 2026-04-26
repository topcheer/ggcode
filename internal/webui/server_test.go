package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
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
