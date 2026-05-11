package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestRequireAuth_BearerToken(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)

	handler := s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Valid Bearer token
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+s.Token())
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200 with valid Bearer token, got %d", w.Code)
	}
}

func TestRequireAuth_QueryParam(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)

	handler := s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Valid token via query parameter
	req := httptest.NewRequest("GET", "/api/test?token="+s.Token(), nil)
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200 with valid query token, got %d", w.Code)
	}
}

func TestRequireAuth_NoToken(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)

	handler := s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// No token at all
	req := httptest.NewRequest("GET", "/api/test", nil)
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Code != 401 {
		t.Fatalf("expected 401 without token, got %d", w.Code)
	}
}

func TestRequireAuth_WrongToken(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)

	handler := s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Wrong Bearer token
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Code != 401 {
		t.Fatalf("expected 401 with wrong token, got %d", w.Code)
	}

	// Wrong query token
	req2 := httptest.NewRequest("GET", "/api/test?token=wrong", nil)
	w2 := httptest.NewRecorder()
	handler(w2, req2)
	if w2.Code != 401 {
		t.Fatalf("expected 401 with wrong query token, got %d", w2.Code)
	}
}

func TestRequireAuth_PartialBearer(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)

	handler := s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Just "Bearer " without actual token
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Authorization", "Bearer ")
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Code != 401 {
		t.Fatalf("expected 401 with empty Bearer, got %d", w.Code)
	}
}

func TestGenerateAuthToken_Uniqueness(t *testing.T) {
	tokens := make(map[string]bool)
	for i := 0; i < 100; i++ {
		tok := generateAuthToken()
		if len(tok) != 64 {
			t.Fatalf("expected 64-char hex token, got len=%d", len(tok))
		}
		if tokens[tok] {
			t.Fatal("duplicate token generated")
		}
		tokens[tok] = true
	}
}

func TestToken_Method(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)

	token := s.Token()
	if token == "" {
		t.Fatal("expected non-empty token")
	}
	if len(token) != 64 {
		t.Fatalf("expected 64-char token, got %d", len(token))
	}
	// Must be hex
	for _, c := range token {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("token contains non-hex char: %c", c)
		}
	}
}

func TestNewServer_GeneratesToken(t *testing.T) {
	cfg := config.DefaultConfig()
	s1 := NewServer(cfg)
	s2 := NewServer(cfg)
	if s1.Token() == s2.Token() {
		t.Fatal("two servers should have different tokens")
	}
}

func TestRequireAuth_WebSocketQueryParam(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)

	called := false
	handler := s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	// WebSocket upgrade requests use query params
	req := httptest.NewRequest("GET", "/api/chat/ws?token="+s.Token(), nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	w := httptest.NewRecorder()
	handler(w, req)
	if !called {
		t.Fatal("handler was not called with valid query token")
	}
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestSanitizeConfigForAPI(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Vendor = "test-vendor"
	cfg.Vendors = map[string]config.VendorConfig{
		"myvendor": {
			APIKey: "super-secret-key",
			Endpoints: map[string]config.EndpointConfig{
				"prod": {
					APIKey: "endpoint-secret-key",
				},
			},
		},
	}
	cfg.MCPServers = []config.MCPServerConfig{
		{
			Name:    "myserver",
			Command: "node",
			Args:    []string{"server.js"},
			Env:     map[string]string{"SECRET": "value"},
			Headers: map[string]string{"X-Auth": "token"},
		},
	}

	result := sanitizeConfigForAPI(cfg)

	// Vendor should still be present
	if result["vendor"] != "test-vendor" {
		t.Fatalf("expected vendor=test-vendor, got %v", result["vendor"])
	}

	// Check vendor API key is masked
	vendors := result["vendors"].(map[string]interface{})
	mv := vendors["myvendor"].(map[string]interface{})
	if mv["api_key"] != "***" {
		t.Errorf("expected vendor api_key masked, got %v", mv["api_key"])
	}

	// Check MCP env/headers are removed (MCPServers is a slice)
	servers := result["mcp_servers"].([]interface{})
	ms := servers[0].(map[string]interface{})
	if _, ok := ms["env"]; ok {
		t.Error("expected env to be removed from MCP config")
	}
	if _, ok := ms["headers"]; ok {
		t.Error("expected headers to be removed from MCP config")
	}
	if !strings.Contains(strings.ToLower(getStringField(ms, "command")), "node") {
		t.Error("expected command to still be present")
	}
}

func TestSanitizeConfigForAPI_NoKeys(t *testing.T) {
	cfg := config.DefaultConfig()
	result := sanitizeConfigForAPI(cfg)

	vendors := result["vendors"].(map[string]interface{})
	// DefaultConfig has preset vendors — verify keys are masked
	for name, v := range vendors {
		vmap := v.(map[string]interface{})
		if ak, ok := vmap["api_key"]; ok {
			if ak != "***" {
				t.Errorf("vendor %s: expected api_key masked, got %v", name, ak)
			}
		}
	}
}

func getStringField(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
