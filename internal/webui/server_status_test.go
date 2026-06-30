package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestHandleStatusWithCallback(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)

	s.SetStatusFn(func() RuntimeStatus {
		return RuntimeStatus{
			PID:            12345,
			Workspace:      "/test/workspace",
			AgentBusy:      true,
			PermissionMode: "auto",
			Vendor:         "openai",
			Endpoint:       "test-endpoint",
			Model:          "gpt-4",
			Language:       "en",
			IMAdapters: []IMAdapterInfo{
				{Name: "slack", Type: "slack", Online: true, Channel: "#general"},
				{Name: "telegram", Type: "telegram", Online: false},
			},
			MobileConn: MobileConnInfo{
				Connected: true,
				SessionID: "sess-abc-123",
				RelayURL:  "wss://relay.example.com/room/test",
			},
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Authorization", "Bearer "+s.Token())
	w := httptest.NewRecorder()
	s.handleStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var st RuntimeStatus
	if err := json.NewDecoder(w.Body).Decode(&st); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if st.PID != 12345 {
		t.Errorf("PID = %d, want 12345", st.PID)
	}
	if st.Workspace != "/test/workspace" {
		t.Errorf("Workspace = %q, want /test/workspace", st.Workspace)
	}
	if !st.AgentBusy {
		t.Error("AgentBusy should be true")
	}
	if st.PermissionMode != "auto" {
		t.Errorf("PermissionMode = %q, want auto", st.PermissionMode)
	}
	if st.Vendor != "openai" {
		t.Errorf("Vendor = %q, want openai", st.Vendor)
	}
	if len(st.IMAdapters) != 2 {
		t.Fatalf("expected 2 IM adapters, got %d", len(st.IMAdapters))
	}
	if st.IMAdapters[0].Name != "slack" || !st.IMAdapters[0].Online {
		t.Errorf("first adapter: %+v", st.IMAdapters[0])
	}
	if !st.MobileConn.Connected {
		t.Error("Mobile should be connected")
	}
	if st.MobileConn.SessionID != "sess-abc-123" {
		t.Errorf("SessionID = %q, want sess-abc-123", st.MobileConn.SessionID)
	}
}

func TestHandleStatusWithoutCallback(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	// Don't set statusFn — should fall back to basic info

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Authorization", "Bearer "+s.Token())
	w := httptest.NewRecorder()
	s.handleStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var st RuntimeStatus
	if err := json.NewDecoder(w.Body).Decode(&st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Should have zero values, not an error
	if st.AgentBusy {
		t.Error("AgentBusy should be false without callback")
	}
}

func TestHandleStatusWrongMethod(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)

	req := httptest.NewRequest(http.MethodPost, "/api/status", nil)
	req.Header.Set("Authorization", "Bearer "+s.Token())
	w := httptest.NewRecorder()
	s.handleStatus(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestSetStatusFn(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)

	if s.statusFn != nil {
		t.Error("statusFn should be nil initially")
	}

	fn := func() RuntimeStatus {
		return RuntimeStatus{PID: 99}
	}
	s.SetStatusFn(fn)

	if s.statusFn == nil {
		t.Fatal("statusFn should be set")
	}
	result := s.statusFn()
	if result.PID != 99 {
		t.Errorf("PID = %d, want 99", result.PID)
	}
}

func TestRuntimeStatusJSONTags(t *testing.T) {
	// Verify all fields have proper json tags for external consumption
	st := RuntimeStatus{
		PID:            1,
		Workspace:      "/ws",
		AgentBusy:      true,
		PermissionMode: "auto",
		Vendor:         "v",
		Endpoint:       "e",
		Model:          "m",
		Language:       "l",
	}

	data, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	jsonStr := string(data)
	expectedKeys := []string{
		`"pid"`, `"workspace"`, `"agent_busy"`, `"permission_mode"`,
		`"vendor"`, `"endpoint"`, `"model"`, `"language"`,
		`"im_adapters"`, `"mobile"`,
	}
	for _, key := range expectedKeys {
		if !contains(jsonStr, key) {
			t.Errorf("JSON output missing %s: %s", key, jsonStr)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
