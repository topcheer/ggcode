package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func newTestServerWithSave(t *testing.T) (*Server, string) {
	t.Helper()
	cfg := config.DefaultConfig()
	tmpDir := t.TempDir()
	cfg.FilePath = filepath.Join(tmpDir, "ggcode.yaml")
	cfg.Vendors = map[string]config.VendorConfig{
		"testvendor": {
			DisplayName: "Test Vendor",
			Endpoints: map[string]config.EndpointConfig{
				"prod": {Protocol: "openai", BaseURL: "https://api.test.com"},
			},
		},
	}
	// Point active vendor to one that exists so Validate passes
	cfg.Vendor = "testvendor"
	cfg.Endpoint = "prod"
	if err := cfg.Save(); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	s := NewServer(cfg)
	return s, tmpDir
}

func jsonBody(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

// ── Endpoints ────────────────────────────────────────────────────────

func TestHandleEndpoints_Get(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := httptest.NewRequest(http.MethodGet, "/api/vendors/testvendor/endpoints", nil)
	req.SetPathValue("vendor", "testvendor")
	w := httptest.NewRecorder()
	s.handleEndpoints(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleEndpoints_Get_VendorNotFound(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := httptest.NewRequest(http.MethodGet, "/api/vendors/nonexistent/endpoints", nil)
	req.SetPathValue("vendor", "nonexistent")
	w := httptest.NewRecorder()
	s.handleEndpoints(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleEndpoints_Post(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := jsonBody(http.MethodPost, "/api/vendors/testvendor/endpoints",
		`{"name":"staging","protocol":"openai","base_url":"https://staging.test.com"}`)
	req.SetPathValue("vendor", "testvendor")
	w := httptest.NewRecorder()
	s.handleEndpoints(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleEndpoints_Post_BadJSON(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := jsonBody(http.MethodPost, "/api/vendors/testvendor/endpoints", "not json")
	req.SetPathValue("vendor", "testvendor")
	w := httptest.NewRecorder()
	s.handleEndpoints(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleEndpoints_MethodNotAllowed(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := httptest.NewRequest(http.MethodPatch, "/api/vendors/testvendor/endpoints", nil)
	req.SetPathValue("vendor", "testvendor")
	w := httptest.NewRecorder()
	s.handleEndpoints(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// ── EndpointDetail ───────────────────────────────────────────────────

func TestHandleEndpointDetail_Get(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := httptest.NewRequest(http.MethodGet, "/api/vendors/testvendor/endpoints/prod", nil)
	req.SetPathValue("vendor", "testvendor")
	req.SetPathValue("endpoint", "prod")
	w := httptest.NewRecorder()
	s.handleEndpointDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleEndpointDetail_Get_VendorNotFound(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := httptest.NewRequest(http.MethodGet, "/api/vendors/nope/endpoints/prod", nil)
	req.SetPathValue("vendor", "nope")
	req.SetPathValue("endpoint", "prod")
	w := httptest.NewRecorder()
	s.handleEndpointDetail(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleEndpointDetail_Get_EndpointNotFound(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := httptest.NewRequest(http.MethodGet, "/api/vendors/testvendor/endpoints/nope", nil)
	req.SetPathValue("vendor", "testvendor")
	req.SetPathValue("endpoint", "nope")
	w := httptest.NewRecorder()
	s.handleEndpointDetail(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleEndpointDetail_Delete(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	// First add an endpoint
	addReq := jsonBody(http.MethodPost, "/api/vendors/testvendor/endpoints",
		`{"name":"staging","protocol":"openai","base_url":"https://staging.test.com"}`)
	addReq.SetPathValue("vendor", "testvendor")
	w := httptest.NewRecorder()
	s.handleEndpoints(w, addReq)
	if w.Code != http.StatusOK {
		t.Fatalf("add: expected 200, got %d", w.Code)
	}

	// Now delete it
	delReq := httptest.NewRequest(http.MethodDelete, "/api/vendors/testvendor/endpoints/staging", nil)
	delReq.SetPathValue("vendor", "testvendor")
	delReq.SetPathValue("endpoint", "staging")
	w2 := httptest.NewRecorder()
	s.handleEndpointDetail(w2, delReq)

	if w2.Code != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d: %s", w2.Code, w2.Body.String())
	}
}

func TestHandleEndpointDetail_MethodNotAllowed(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := httptest.NewRequest(http.MethodPatch, "/api/vendors/testvendor/endpoints/prod", nil)
	req.SetPathValue("vendor", "testvendor")
	req.SetPathValue("endpoint", "prod")
	w := httptest.NewRecorder()
	s.handleEndpointDetail(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// ── APIKey ───────────────────────────────────────────────────────────

func TestHandleAPIKey_Get(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := httptest.NewRequest(http.MethodGet, "/api/vendors/testvendor/endpoints/prod/apikey", nil)
	req.SetPathValue("vendor", "testvendor")
	req.SetPathValue("endpoint", "prod")
	w := httptest.NewRecorder()
	s.handleAPIKey(w, req)
	t.Logf("APIKey GET: status=%d body=%s", w.Code, w.Body.String())
}

func TestHandleAPIKey_Put(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := jsonBody(http.MethodPut, "/api/vendors/testvendor/endpoints/prod/apikey",
		`{"api_key":"sk-test-123"}`)
	req.SetPathValue("vendor", "testvendor")
	req.SetPathValue("endpoint", "prod")
	w := httptest.NewRecorder()
	s.handleAPIKey(w, req)
	t.Logf("APIKey PUT: status=%d body=%s", w.Code, w.Body.String())
}

// ── General ──────────────────────────────────────────────────────────

func TestHandleGeneral_Get(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := httptest.NewRequest(http.MethodGet, "/api/general", nil)
	w := httptest.NewRecorder()
	s.handleGeneral(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
}

func TestHandleGeneral_Put(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := jsonBody(http.MethodPut, "/api/general",
		`{"language":"en","default_mode":"agent","sidebar_visible":true}`)
	w := httptest.NewRecorder()
	s.handleGeneral(w, req)
	t.Logf("General PUT: status=%d body=%s", w.Code, w.Body.String())
}

func TestHandleGeneral_MethodNotAllowed(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := httptest.NewRequest(http.MethodPost, "/api/general", nil)
	w := httptest.NewRecorder()
	s.handleGeneral(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// ── VendorDetail ─────────────────────────────────────────────────────

func TestHandleVendorDetail_Get(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := httptest.NewRequest(http.MethodGet, "/api/vendors/testvendor", nil)
	req.SetPathValue("vendor", "testvendor")
	w := httptest.NewRecorder()
	s.handleVendorDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleVendorDetail_Get_NotFound(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := httptest.NewRequest(http.MethodGet, "/api/vendors/nonexistent", nil)
	req.SetPathValue("vendor", "nonexistent")
	w := httptest.NewRecorder()
	s.handleVendorDetail(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleVendorDetail_Delete(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/vendors/testvendor", nil)
	req.SetPathValue("vendor", "testvendor")
	w := httptest.NewRecorder()
	s.handleVendorDetail(w, req)
	t.Logf("VendorDetail DELETE: status=%d body=%s", w.Code, w.Body.String())
}

func TestHandleVendorDetail_MethodNotAllowed(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := httptest.NewRequest(http.MethodPatch, "/api/vendors/testvendor", nil)
	req.SetPathValue("vendor", "testvendor")
	w := httptest.NewRecorder()
	s.handleVendorDetail(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// ── Vendors ──────────────────────────────────────────────────────────

func TestHandleVendors_Post(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := jsonBody(http.MethodPost, "/api/vendors",
		`{"name":"newvendor","endpoints":{"default":{"protocol":"openai","base_url":"https://new.test.com"}}}`)
	w := httptest.NewRecorder()
	s.handleVendors(w, req)
	t.Logf("Vendors POST: status=%d body=%s", w.Code, w.Body.String())
}

func TestHandleVendors_MethodNotAllowed(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := httptest.NewRequest(http.MethodPatch, "/api/vendors", nil)
	w := httptest.NewRecorder()
	s.handleVendors(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// ── SPA ──────────────────────────────────────────────────────────────

func TestServeSPA_Index(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	s.serveSPA(w, req)
	// SPA returns embedded index.html
	t.Logf("SPA /: status=%d", w.Code)
}

func TestServeSPA_SubPath(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := httptest.NewRequest(http.MethodGet, "/some/path", nil)
	w := httptest.NewRecorder()
	s.serveSPA(w, req)
	t.Logf("SPA /some/path: status=%d", w.Code)
}

// ── Active Selection ─────────────────────────────────────────────────

func TestHandleActiveSelection_Put(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := jsonBody(http.MethodPut, "/api/config/active",
		`{"vendor":"testvendor","endpoint":"prod"}`)
	w := httptest.NewRecorder()
	s.handleActiveSelection(w, req)
	t.Logf("ActiveSelection PUT: status=%d body=%s", w.Code, w.Body.String())
}

func TestHandleActiveSelection_MethodNotAllowed(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := httptest.NewRequest(http.MethodPost, "/api/config/active", nil)
	w := httptest.NewRecorder()
	s.handleActiveSelection(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// ── MCP ──────────────────────────────────────────────────────────────

func TestHandleMCP_Get(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	s.SetMCPStatusFn(func() map[string]MCPRuntimeStatus { return nil })
	req := httptest.NewRequest(http.MethodGet, "/api/mcp", nil)
	w := httptest.NewRecorder()
	s.handleMCP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleMCPStatus_Get(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	s.SetMCPStatusFn(func() map[string]MCPRuntimeStatus {
		return map[string]MCPRuntimeStatus{"test": {Connected: true}}
	})
	req := httptest.NewRequest(http.MethodGet, "/api/mcp/status", nil)
	w := httptest.NewRecorder()
	s.handleMCPStatus(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// ── IM ───────────────────────────────────────────────────────────────

func TestHandleIM_Get(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := httptest.NewRequest(http.MethodGet, "/api/im", nil)
	w := httptest.NewRecorder()
	s.handleIM(w, req)
	t.Logf("IM GET: status=%d", w.Code)
}

func TestHandleIMStatus_Get(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	s.SetIMStatusFn(func() []IMRuntimeStatus { return nil })
	req := httptest.NewRequest(http.MethodGet, "/api/im/status", nil)
	w := httptest.NewRecorder()
	s.handleIMStatus(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// ── A2A ──────────────────────────────────────────────────────────────

func TestHandleA2A_Get(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := httptest.NewRequest(http.MethodGet, "/api/a2a", nil)
	w := httptest.NewRecorder()
	s.handleA2A(w, req)
	t.Logf("A2A GET: status=%d", w.Code)
}

func TestHandleA2ADiscover_Post(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	s.SetA2ADiscoverFn(func() []A2ADiscoveredInstance { return nil })
	req := httptest.NewRequest(http.MethodPost, "/api/a2a/discover", nil)
	w := httptest.NewRecorder()
	s.handleA2ADiscover(w, req)
	t.Logf("A2A Discover: status=%d", w.Code)
}

// ── Config ───────────────────────────────────────────────────────────

func TestHandleConfig_Get(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()
	s.handleConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// ── Restarts ─────────────────────────────────────────────────────────

func TestHandleRestart_Post(t *testing.T) {
	s, _ := newTestServerWithSave(t)
	restarted := false
	s.SetRestartFn(func() { restarted = true })
	req := httptest.NewRequest(http.MethodPost, "/api/restart", nil)
	w := httptest.NewRecorder()
	s.handleRestart(w, req)

	t.Logf("Restart: status=%d restarted=%v", w.Code, restarted)
}

// ── file existence ───────────────────────────────────────────────────

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, map[string]string{"hello": "world"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusBadRequest, "bad request")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func init() {
	// Ensure os.NopCloser is available via io.NopCloser
	_ = os.DevNull
}
