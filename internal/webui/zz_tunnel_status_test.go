package webui

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// 钉测契约(侧栏双隧道入口):GET /api/tunnel/status
// ① nil fn(独立 webui 模式)回落默认形态(share/et 双键,active=false)
// ② fn 注入后透传活快照(share QR/connected + et URL)
func TestTunnelStatusEndpoint(t *testing.T) {
	// ① nil fn 回落
	s := NewServer(config.DefaultConfig())
	s.SetSessionStore(nil, t.TempDir())
	rec := httptest.NewRecorder()
	s.handleTunnelStatus(rec, httptest.NewRequest("GET", "/api/tunnel/status", nil))
	if rec.Code != 200 {
		t.Fatalf("nil-fn status = %d", rec.Code)
	}
	var d map[string]map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatalf("body not JSON: %s", rec.Body.String())
	}
	if d["share"] == nil || d["et"] == nil {
		t.Fatalf("missing share/et keys: %v", d)
	}
	if d["share"]["active"] != false || d["et"]["active"] != false {
		t.Fatalf("default shape should be inactive: %v", d)
	}

	// ② fn 注入透传
	s2 := NewServer(config.DefaultConfig())
	s2.SetTunnelStatusFn(func() map[string]any {
		return map[string]any{
			"share": map[string]any{"active": true, "connected": true, "qrcode_png_b64": "AAAA"},
			"et":    map[string]any{"active": true, "url": "https://x.t.etun.uk#token=abc"},
		}
	})
	rec2 := httptest.NewRecorder()
	s2.handleTunnelStatus(rec2, httptest.NewRequest("GET", "/api/tunnel/status", nil))
	var d2 map[string]map[string]interface{}
	if err := json.Unmarshal(rec2.Body.Bytes(), &d2); err != nil {
		t.Fatalf("fn body not JSON: %s", rec2.Body.String())
	}
	if d2["share"]["connected"] != true || d2["et"]["active"] != true {
		t.Fatalf("fn passthrough broken: %v", d2)
	}

	// ③ method guard
	rec3 := httptest.NewRecorder()
	s.handleTunnelStatus(rec3, httptest.NewRequest("POST", "/api/tunnel/status", nil))
	if rec3.Code != 405 {
		t.Fatalf("POST should 405, got %d", rec3.Code)
	}
}
