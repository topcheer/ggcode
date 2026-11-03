package webui

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

func authGet(serverURL string, token string) (*http.Response, error) {
	req, err := http.NewRequest("GET", serverURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return http.DefaultClient.Do(req)
}

func authPost(serverURL, token, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest("POST", serverURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return http.DefaultClient.Do(req)
}

func authPut(serverURL, token string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest("PUT", serverURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func authDelete(serverURL, token string) (*http.Request, error) {
	req, err := http.NewRequest("DELETE", serverURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return req, nil
}

func TestE2EConfigGetStructure(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Vendor = "test-vendor"
	cfg.Endpoint = "test-ep"
	cfg.Model = "test-model"
	cfg.Language = "zh"

	s := NewServer(cfg)
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	resp, err := authGet(srv.URL+"/api/config", s.Token())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]json.RawMessage
	json.NewDecoder(resp.Body).Decode(&result)

	expectedKeys := []string{"vendor", "endpoint", "model", "language"}
	for _, k := range expectedKeys {
		if _, ok := result[k]; !ok {
			t.Errorf("missing key: %s", k)
		}
	}
}

func TestE2EActiveSelectionGetPut(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Vendor = "initial-vendor"
	cfg.Endpoint = "initial-ep"

	s := NewServer(cfg)
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	resp, err := authGet(srv.URL+"/api/config/active", s.Token())
	if err != nil {
		t.Fatal(err)
	}
	var initial map[string]string
	json.NewDecoder(resp.Body).Decode(&initial)
	resp.Body.Close()
	if initial["vendor"] != "initial-vendor" {
		t.Errorf("expected initial-vendor, got %s", initial["vendor"])
	}

	body := `{"vendor":"new-vendor","endpoint":"new-ep","model":"new-model"}`
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/config/active", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()

}

func TestE2EVendorsList(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	resp, err := authGet(srv.URL+"/api/vendors", s.Token())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if len(result) == 0 {
		t.Error("expected at least one vendor")
	}

	for _, v := range result {
		if _, ok := v["id"]; !ok {
			t.Errorf("vendor missing 'id': %v", v)
		}
	}
}

func TestE2EVendorDetail(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	resp, err := authGet(srv.URL+"/api/vendors", s.Token())
	if err != nil {
		t.Fatal(err)
	}
	var vendors []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&vendors)
	resp.Body.Close()

	if len(vendors) == 0 {
		t.Skip("no vendors available")
	}
	vendorName := vendors[0]["id"].(string)

	resp, err = authGet(srv.URL+"/api/vendors/"+vendorName, s.Token())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 for vendor %s, got %d", vendorName, resp.StatusCode)
	}
}

func TestE2EMCPConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.MCPServers = []config.MCPServerConfig{
		{Name: "fs", Type: "stdio", Command: "npx", Args: []string{"-y", "@mcp/fs"}},
	}
	s := NewServer(cfg)
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	resp, err := authGet(srv.URL+"/api/mcp", s.Token())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if len(result) != 1 {
		t.Fatalf("expected 1 server, got %d", len(result))
	}
	if result[0]["name"] != "fs" {
		t.Errorf("expected name 'fs', got %v", result[0]["name"])
	}
}

func TestE2EIMConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.IM.Enabled = true
	cfg.IM.Adapters = map[string]config.IMAdapterConfig{
		"qq": {Enabled: true, Platform: "qq"},
	}
	s := NewServer(cfg)
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	resp, err := authGet(srv.URL+"/api/im", s.Token())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if result["enabled"] != true {
		t.Error("expected enabled=true")
	}
}

func TestE2EA2AConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.A2A.Disabled = false
	cfg.A2A.Host = "0.0.0.0"
	cfg.A2A.Port = 9999
	s := NewServer(cfg)
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	resp, err := authGet(srv.URL+"/api/a2a", s.Token())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if result["disabled"] == true {
		t.Error("A2A should not be disabled")
	}
	if result["host"] != "0.0.0.0" {
		t.Errorf("expected host 0.0.0.0, got %v", result["host"])
	}
	if result["port"] != float64(9999) {
		t.Errorf("expected port 9999, got %v", result["port"])
	}
}

func TestE2EGeneralConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Language = "en"
	cfg.DefaultMode = "auto"
	s := NewServer(cfg)
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	resp, err := authGet(srv.URL+"/api/general", s.Token())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if result["language"] != "en" {
		t.Errorf("expected language en, got %v", result["language"])
	}
	if result["default_mode"] != "auto" {
		t.Errorf("expected default_mode auto, got %v", result["default_mode"])
	}
}

func TestE2EImpersonateGet(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	resp, err := authGet(srv.URL+"/api/impersonate", s.Token())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if _, ok := result["current"]; !ok {
		t.Error("missing 'current' key")
	}
	presets, ok := result["presets"].([]interface{})
	if !ok || len(presets) == 0 {
		t.Error("expected non-empty presets list")
	}
}

func TestE2EMethodNotAllowed(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	cases := []struct {
		method string
		path   string
	}{
		{"DELETE", "/api/config"},
		{"DELETE", "/api/config/active"},
		{"DELETE", "/api/vendors"},
		{"DELETE", "/api/mcp"},
		{"POST", "/api/sessions"},
		{"DELETE", "/api/sessions/123"},
		{"PUT", "/api/chat/history"},
	}
	for _, tc := range cases {
		req, _ := http.NewRequest(tc.method, srv.URL+tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+s.Token())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Logf("%s %s: %v", tc.method, tc.path, err)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: expected 405, got %d", tc.method, tc.path, resp.StatusCode)
		}
	}
}

func TestE2ESessionsLifecycle(t *testing.T) {
	dir := t.TempDir()
	store, _ := session.NewJSONLStore(dir)
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetSessionStore(store, "/workspace")
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	resp, err := authGet(srv.URL+"/api/sessions", s.Token())
	if err != nil {
		t.Fatal(err)
	}
	var listResult map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&listResult)
	resp.Body.Close()
	if listResult["total"] != float64(0) {
		t.Errorf("expected 0 sessions, got %v", listResult["total"])
	}

	for i := 0; i < 5; i++ {
		ses := &session.Session{
			ID:        fmt.Sprintf("20260401-%06d", i),
			Title:     fmt.Sprintf("Session %d", i),
			Workspace: "/workspace",
			Vendor:    "test",
			Model:     "model-v1",
			Messages: []provider.Message{
				{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("msg %d", i)}}},
				{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("reply %d", i)}}},
			},
		}
		ses.CreatedAt = time.Now().Add(-time.Duration(5-i) * time.Hour)
		ses.UpdatedAt = time.Now().Add(-time.Duration(5-i) * time.Hour)
		_ = store.Save(ses)
		_ = store.AppendMetaToDisk(ses)
		if len(ses.Messages) > 0 {
			_ = store.AppendMessagesBatchToDisk(ses, ses.Messages)
		}
	}

	resp, err = authGet(srv.URL+"/api/sessions", s.Token())
	if err != nil {
		t.Fatal(err)
	}
	json.NewDecoder(resp.Body).Decode(&listResult)
	resp.Body.Close()
	if listResult["total"] != float64(5) {
		t.Errorf("expected 5 sessions, got %v", listResult["total"])
	}

	resp, err = authGet(srv.URL+"/api/sessions/20260401-000000", s.Token())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var detail map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&detail)
	if detail["title"] != "Session 0" {
		t.Errorf("expected 'Session 0', got %v", detail["title"])
	}
	msgs := detail["messages"].([]interface{})
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages, got %d", len(msgs))
	}

	resp, err = authGet(srv.URL+"/api/sessions/nonexistent", s.Token())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestE2ESessionsWorkspaceFiltering(t *testing.T) {
	dir, err := os.MkdirTemp("", "wsfilter-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	store, _ := session.NewJSONLStore(dir)

	for i, ws := range []string{"/ws-a", "/ws-b", "/ws-c"} {
		ses := &session.Session{
			ID:        fmt.Sprintf("ws-test-%d", i),
			Title:     fmt.Sprintf("Session in %s", ws),
			Workspace: ws,
			Vendor:    "test",
			Messages: []provider.Message{
				{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}},
			},
		}
		ses.CreatedAt = time.Now()
		ses.UpdatedAt = time.Now()
		_ = store.Save(ses)
		_ = store.AppendMetaToDisk(ses)
		if len(ses.Messages) > 0 {
			_ = store.AppendMessagesBatchToDisk(ses, ses.Messages)
		}
	}

	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetSessionStore(store, "/ws-a")
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	resp, err := authGet(srv.URL+"/api/sessions", s.Token())
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()

	if result["current_workspace"] != "/ws-a" {
		t.Errorf("expected /ws-a, got %v", result["current_workspace"])
	}

	if result["total"] != float64(3) {
		t.Errorf("expected total 3, got %v", result["total"])
	}
	groups := result["groups"].([]interface{})
	if len(groups) != 3 {
		t.Errorf("expected 3 groups, got %d", len(groups))
	}

	var currentFound bool
	for _, g := range groups {
		group := g.(map[string]interface{})
		if group["current"].(bool) {
			currentFound = true
			if group["workspace"] != "/ws-a" {
				t.Errorf("current group should be /ws-a, got %v", group["workspace"])
			}
		}
	}
	if !currentFound {
		t.Error("no group marked as current")
	}
}

func TestE2EChatWSFullLifecycle(t *testing.T) {
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

	ws.WriteJSON(map[string]string{"type": "user_message", "text": "explain goroutines"})

	var ack map[string]interface{}
	ws.ReadJSON(&ack)
	if ack["type"] != "user_ack" {
		t.Fatalf("expected user_ack, got %v", ack["type"])
	}
	if ack["text"] != "explain goroutines" {
		t.Errorf("expected echo of sent text, got %v", ack["text"])
	}

	go func() {
		time.Sleep(30 * time.Millisecond)

		bridge.broadcastEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "Goroutines "})
		bridge.broadcastEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "are lightweight "})
		bridge.broadcastEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "threads."})

		bridge.broadcastEvent(provider.StreamEvent{
			Type: provider.StreamEventToolCallDone,
			Tool: provider.ToolCallDelta{
				ID: "tc1", Name: "run_command",
				Arguments: json.RawMessage(`{"command":"go version"}`),
			},
		})

		bridge.broadcastEvent(provider.StreamEvent{
			Type:   provider.StreamEventToolResult,
			Tool:   provider.ToolCallDelta{Name: "run_command"},
			Result: "go version go1.22.0",
		})

		bridge.broadcastEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "Go 1.22 is installed."})

		bridge.broadcastEvent(provider.StreamEvent{
			Type:  provider.StreamEventDone,
			Usage: &provider.TokenUsage{InputTokens: 150, OutputTokens: 200},
		})
	}()

	var textParts []string
	var gotToolCall, gotToolResult, gotDone bool

	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		var msg map[string]interface{}
		if err := ws.ReadJSON(&msg); err != nil {
			t.Fatalf("read error: %v", err)
		}
		switch msg["type"] {
		case "text_delta":
			textParts = append(textParts, msg["text"].(string))
		case "tool_call":
			gotToolCall = true
			if msg["name"] != "run_command" {
				t.Errorf("tool_call name mismatch: %v", msg["name"])
			}
			if msg["id"] != "tc1" {
				t.Errorf("tool_call id mismatch: %v", msg["id"])
			}
			if msg["arguments"] == nil {
				t.Error("tool_call missing arguments")
			}
		case "tool_result":
			gotToolResult = true
			if msg["result"] != "go version go1.22.0" {
				t.Errorf("tool_result mismatch: %v", msg["result"])
			}
		case "done":
			gotDone = true
			usage := msg["usage"].(map[string]interface{})
			if usage["input_tokens"] != float64(150) {
				t.Errorf("input_tokens mismatch: %v", usage["input_tokens"])
			}
			if usage["output_tokens"] != float64(200) {
				t.Errorf("output_tokens mismatch: %v", usage["output_tokens"])
			}
		case "error":
			t.Fatalf("unexpected error: %v", msg["error"])
		}
		if gotDone {
			break
		}
	}

	fullText := strings.Join(textParts, "")
	expected := "Goroutines are lightweight threads.Go 1.22 is installed."
	if fullText != expected {
		t.Errorf("text mismatch:\ngot:      %q\nexpected: %q", fullText, expected)
	}
	if !gotToolCall {
		t.Error("missing tool_call event")
	}
	if !gotToolResult {
		t.Error("missing tool_result event")
	}
}

func TestE2EChatWSErrorEvent(t *testing.T) {
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

	ws.WriteJSON(map[string]string{"type": "user_message", "text": "cause error"})
	var ack map[string]interface{}
	ws.ReadJSON(&ack)

	go func() {
		time.Sleep(30 * time.Millisecond)
		bridge.broadcastEvent(provider.StreamEvent{
			Type:  provider.StreamEventError,
			Error: fmt.Errorf("rate limit exceeded"),
		})
		bridge.broadcastEvent(provider.StreamEvent{Type: provider.StreamEventDone})
	}()

	var gotError bool
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		var msg map[string]interface{}
		ws.ReadJSON(&msg)
		if msg["type"] == "error" {
			gotError = true
			if !strings.Contains(msg["error"].(string), "rate limit") {
				t.Errorf("error message mismatch: %v", msg["error"])
			}
		}
		if msg["type"] == "done" {
			break
		}
	}
	if !gotError {
		t.Error("expected error event")
	}
}

func TestE2EChatWSThreeConnectionBroadcast(t *testing.T) {
	bridge := &mockChatBridge{}
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetChatBridge(bridge)
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/chat/ws?token=" + s.Token()

	conns := make([]*websocket.Conn, 3)
	for i := range conns {
		ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("conn %d: %v", i, err)
		}
		defer ws.Close()
		conns[i] = ws
	}

	conns[0].WriteJSON(map[string]string{"type": "user_message", "text": "broadcast test"})
	var ack map[string]interface{}
	conns[0].ReadJSON(&ack)

	events := []provider.StreamEvent{
		{Type: provider.StreamEventText, Text: "chunk1 "},
		{Type: provider.StreamEventText, Text: "chunk2 "},
		{Type: provider.StreamEventToolCallDone, Tool: provider.ToolCallDelta{ID: "t1", Name: "test_tool", Arguments: json.RawMessage(`{}`)}},
		{Type: provider.StreamEventToolResult, Tool: provider.ToolCallDelta{Name: "test_tool"}, Result: "ok"},
		{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{InputTokens: 5, OutputTokens: 10}},
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		for _, e := range events {
			bridge.broadcastEvent(e)
		}
	}()

	for i, ws := range conns {
		var received int
		ws.SetReadDeadline(time.Now().Add(3 * time.Second))
		for {
			var msg map[string]interface{}
			if err := ws.ReadJSON(&msg); err != nil {
				t.Fatalf("conn %d read error after %d events: %v", i, received, err)
			}
			received++
			if msg["type"] == "done" {
				break
			}
		}
		if received != 5 {
			t.Errorf("conn %d: expected 5 events, got %d", i, received)
		}
	}
}

func TestE2EChatWSSequentialMessages(t *testing.T) {
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

	msgs := []string{"first", "second", "third"}
	for _, m := range msgs {
		ws.WriteJSON(map[string]string{"type": "user_message", "text": m})
		var ack map[string]interface{}
		ws.SetReadDeadline(time.Now().Add(2 * time.Second))
		ws.ReadJSON(&ack)
		if ack["type"] != "user_ack" {
			t.Errorf("expected user_ack for '%s', got %v", m, ack["type"])
		}
		if ack["text"] != m {
			t.Errorf("ack text mismatch: expected '%s', got %v", m, ack["text"])
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(bridge.lastContent) == 1 && bridge.lastContent[0].Text == "third" {
			break
		}
		runtime.Gosched()
	}
	if len(bridge.lastContent) != 1 || bridge.lastContent[0].Text != "third" {
		t.Errorf("bridge should have last message, got %v", bridge.lastContent)
	}
}

func TestE2EChatWSMultipleImages(t *testing.T) {
	bridge := &mockChatBridge{}
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetChatBridge(bridge)
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/chat/ws?token=" + s.Token()
	ws, _, _ := websocket.DefaultDialer.Dial(wsURL, nil)
	defer ws.Close()

	ws.WriteJSON(map[string]interface{}{
		"type": "user_message",
		"text": "compare these",
		"images": []map[string]string{
			{"mime": "image/png", "data": "aaaa"},
			{"mime": "image/jpeg", "data": "bbbb"},
			{"mime": "image/gif", "data": "cccc"},
		},
	})

	var ack map[string]interface{}
	ws.ReadJSON(&ack)
	if ack["type"] != "user_ack" {
		t.Fatal("expected user_ack")
	}
	if ack["image_count"] != float64(3) {
		t.Errorf("expected image_count 3, got %v", ack["image_count"])
	}

	if len(bridge.lastContent) != 4 {
		t.Fatalf("expected 4 content blocks, got %d", len(bridge.lastContent))
	}
	for i, block := range bridge.lastContent[1:] {
		if block.Type != "image" {
			t.Errorf("block %d: expected image, got %s", i, block.Type)
		}
	}
}

func TestE2EChatWSInvalidBase64File(t *testing.T) {
	bridge := &mockChatBridge{}
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetChatBridge(bridge)
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/chat/ws?token=" + s.Token()
	ws, _, _ := websocket.DefaultDialer.Dial(wsURL, nil)
	defer ws.Close()

	ws.WriteJSON(map[string]interface{}{
		"type":  "user_message",
		"text":  "check this",
		"files": []map[string]string{{"name": "bad.txt", "mime": "text/plain", "data": "!!invalid-base64!!"}},
	})

	var firstMsg map[string]interface{}
	ws.ReadJSON(&firstMsg)

	if firstMsg["type"] == "error" {

		var secondMsg map[string]interface{}
		ws.ReadJSON(&secondMsg)
		if secondMsg["type"] != "user_ack" {
			t.Errorf("expected user_ack after error, got %v", secondMsg["type"])
		}
	} else if firstMsg["type"] == "user_ack" {

	} else {
		t.Errorf("unexpected message type: %v", firstMsg["type"])
	}

	if len(bridge.lastContent) < 1 || bridge.lastContent[0].Type != "text" {
		t.Errorf("expected text content block, got %v", bridge.lastContent)
	}
}

func TestE2EChatWSMixedAttachments(t *testing.T) {
	bridge := &mockChatBridge{}
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetChatBridge(bridge)
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/chat/ws?token=" + s.Token()
	ws, _, _ := websocket.DefaultDialer.Dial(wsURL, nil)
	defer ws.Close()

	fileContent := base64.StdEncoding.EncodeToString([]byte("hello world"))
	ws.WriteJSON(map[string]interface{}{
		"type":   "user_message",
		"text":   "analyze all",
		"images": []map[string]string{{"mime": "image/png", "data": "iVBORw0KGgo="}},
		"files":  []map[string]string{{"name": "data.txt", "mime": "text/plain", "data": fileContent}},
	})

	var ack map[string]interface{}
	ws.ReadJSON(&ack)
	if ack["type"] != "user_ack" {
		t.Fatal("expected user_ack")
	}
	if ack["image_count"] != float64(1) {
		t.Errorf("expected image_count 1, got %v", ack["image_count"])
	}
	fnames, _ := ack["file_names"].([]interface{})
	if len(fnames) != 1 || fnames[0] != "data.txt" {
		t.Errorf("expected file_names [data.txt], got %v", ack["file_names"])
	}

	deadline := time.Now().Add(2 * time.Second)
	for len(bridge.lastContent) != 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(bridge.lastContent) != 3 {
		t.Errorf("expected 3 content blocks, got %d: %+v", len(bridge.lastContent), bridge.lastContent)
	}
}

func TestE2EChatWSDisconnectCleanup(t *testing.T) {
	bridge := &mockChatBridge{}
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetChatBridge(bridge)
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/chat/ws?token=" + s.Token()

	ws, _, _ := websocket.DefaultDialer.Dial(wsURL, nil)
	ws.Close()

	time.Sleep(100 * time.Millisecond)
	ws2, _, _ := websocket.DefaultDialer.Dial(wsURL, nil)
	defer ws2.Close()

	go func() {
		time.Sleep(50 * time.Millisecond)
		bridge.broadcastEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "still alive"})
		bridge.broadcastEvent(provider.StreamEvent{Type: provider.StreamEventDone})
	}()

	ws2.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg map[string]interface{}
	ws2.ReadJSON(&msg)
	if msg["type"] != "text_delta" || msg["text"] != "still alive" {
		t.Errorf("ws2 should still receive events after ws1 disconnected, got %v", msg)
	}
}

func TestE2EChatWSConcurrentSends(t *testing.T) {
	bridge := &mockChatBridge{}
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetChatBridge(bridge)
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/chat/ws?token=" + s.Token()

	var wg sync.WaitGroup
	var ackCount int32
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Errorf("conn %d dial: %v", idx, err)
				return
			}
			defer ws.Close()

			ws.WriteJSON(map[string]string{
				"type": "user_message",
				"text": fmt.Sprintf("msg-%d", idx),
			})
			ws.SetReadDeadline(time.Now().Add(3 * time.Second))
			var ack map[string]interface{}
			ws.ReadJSON(&ack)
			if ack["type"] == "user_ack" {
				atomic.AddInt32(&ackCount, 1)
			}
		}(i)
	}
	wg.Wait()

	if ackCount != 5 {
		t.Errorf("expected 5 acks, got %d", ackCount)
	}
}

func TestE2EChatHistoryToolContent(t *testing.T) {
	bridge := &mockChatBridge{
		messages: []provider.Message{
			{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "run ls"}}},
			{Role: "assistant", Content: []provider.ContentBlock{
				{Type: "text", Text: "listing files"},
				{Type: "tool_use", ToolName: "run_command", ToolID: "t1", Input: json.RawMessage(`{"command":"ls"}`)},
			}},
			{Role: "user", Content: []provider.ContentBlock{
				{Type: "tool_result", ToolID: "t1", ToolName: "run_command", Output: "file1.go\nfile2.go"},
			}},
			{Role: "assistant", Content: []provider.ContentBlock{
				{Type: "text", Text: "2 files found"},
			}},
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

	if len(result) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(result))
	}

	if result[0]["role"] != "user" {
		t.Error("msg 0 should be user")
	}

	assistantBlocks := result[1]["content"].([]interface{})
	if len(assistantBlocks) != 2 {
		t.Fatalf("expected 2 blocks in assistant msg, got %d", len(assistantBlocks))
	}
	toolBlock := assistantBlocks[1].(map[string]interface{})
	if toolBlock["type"] != "tool_use" {
		t.Errorf("expected tool_use, got %v", toolBlock["type"])
	}
	if toolBlock["tool_name"] != "run_command" {
		t.Errorf("expected run_command, got %v", toolBlock["tool_name"])
	}

	toolResultBlocks := result[2]["content"].([]interface{})
	resultBlock := toolResultBlocks[0].(map[string]interface{})
	if resultBlock["type"] != "tool_result" {
		t.Errorf("expected tool_result, got %v", resultBlock["type"])
	}
	if resultBlock["output"] != "file1.go\nfile2.go" {
		t.Errorf("unexpected output: %v", resultBlock["output"])
	}
}

func TestE2EChatHistoryEmpty(t *testing.T) {
	bridge := &mockChatBridge{messages: nil}
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

	var result []interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if len(result) != 0 {
		t.Errorf("expected empty array, got %v", result)
	}
}

func TestE2ERestartEndpoint(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/restart", nil)
	req.Header.Set("Authorization", "Bearer "+s.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	if result["status"] != "restarting" {
		t.Errorf("expected status 'restarting', got %v", result["status"])
	}

	resp, err = authGet(srv.URL+"/api/restart", s.Token())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 405 {
		t.Errorf("expected 405 for GET /restart, got %d", resp.StatusCode)
	}
}

func TestE2ESPAFallback(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	paths := []string{"/", "/sessions", "/chat", "/config/general"}
	for _, path := range paths {
		resp, err := authGet(srv.URL+path, s.Token())
		if err != nil {
			t.Logf("GET %s: %v", path, err)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode == 500 {
			t.Errorf("GET %s: unexpected 500", path)
		}
	}
}

func TestE2EMCPStatusCallback(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetMCPStatusFn(func() map[string]MCPRuntimeStatus {
		return map[string]MCPRuntimeStatus{
			"test-server": {Connected: true, Tools: []string{"read", "write", "search"}, Disabled: false},
		}
	})
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	resp, err := authGet(srv.URL+"/api/mcp/status", s.Token())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if len(result) == 0 {
		t.Error("expected at least one status entry")
	}
	if _, ok := result["test-server"]; !ok {
		t.Errorf("expected test-server status, got %v", result)
	}
}

func TestE2EIMStatusCallback(t *testing.T) {
	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetIMStatusFn(func() []IMRuntimeStatus {
		return []IMRuntimeStatus{
			{Adapter: "qq", Platform: "qq", Healthy: true, Status: "connected"},
			{Adapter: "discord", Platform: "discord", Healthy: false, Status: "disconnected"},
		}
	})
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	resp, err := authGet(srv.URL+"/api/im/status", s.Token())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if len(result) != 2 {
		t.Errorf("expected 2 statuses, got %d", len(result))
	}
	if result[0]["adapter"] != "qq" {
		t.Errorf("expected adapter qq, got %v", result[0]["adapter"])
	}
	if result[1]["adapter"] != "discord" {
		t.Errorf("expected adapter discord, got %v", result[1]["adapter"])
	}
}
