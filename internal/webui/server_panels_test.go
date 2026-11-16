package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPanelDirectiveFor 钉面板分流判定:panelsDisabled 总闸生效期间,
// 一切命令(含无参面板形态)一律直通(ok=false)——禁用态行为契约。
// 翻回总闸后可恢复原判定表(见 git 历史本测试前版)。
func TestPanelDirectiveFor(t *testing.T) {
	for _, text := range []string{"/model", "/qq", "/sessions", "/effort", "/resume abc123", "/help", "hello"} {
		if _, _, ok := panelDirectiveFor(text); ok {
			t.Errorf("panelsDisabled 生效中: panelDirectiveFor(%q) ok=true, want false(直通)", text)
		}
	}
}

// TestHandleChatCommandPanelDirective 禁用态:无参命令也不进面板,
// 直通命令链(chatCommandFn 收到原文)。
func TestHandleChatCommandPanelDirective(t *testing.T) {
	got := ""
	s := &Server{chatCommandFn: func(text string) error {
		got = text
		return nil
	}}
	rec := httptest.NewRecorder()
	s.handleChatCommand(rec, httptest.NewRequest(http.MethodPost, "/api/chat/commands", strings.NewReader(`{"text":"/model"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got != "/model" {
		t.Errorf("panelsDisabled 生效中: chatCommandFn got %q, want /model 直通", got)
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["status"] != "submitted" {
		t.Errorf("status = %q, want submitted(直通)", resp["status"])
	}
}

// TestHandleChatCommandParamPassThrough 带参命令不进面板分支,直通命令链。
func TestHandleChatCommandParamPassThrough(t *testing.T) {
	got := ""
	s := &Server{chatCommandFn: func(text string) error {
		got = text
		return nil
	}}
	rec := httptest.NewRecorder()
	s.handleChatCommand(rec, httptest.NewRequest(http.MethodPost, "/api/chat/commands", strings.NewReader(`{"text":"/model glm-5.3"}`)))
	if got != "/model glm-5.3" {
		t.Errorf("chatCommandFn got %q, want direct pass-through", got)
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["status"] != "submitted" {
		t.Errorf("status = %q, want submitted", resp["status"])
	}
}

// TestPanelDataPending501 P3/P4 未实现面板数据端点返回 501 占位。
func TestPanelDataPending501(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.handlePanelData(rec, httptest.NewRequest(http.MethodGet, "/api/panel/plugins", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("plugins panel = %d, want 501 pending", rec.Code)
	}
}

// TestPanelDataInjection 注入面优先级钉测:SetPanelDataFn 命中即真数据,
// 未注入(usage/plugins)维持 501 占位(#5714 增量)。
func TestPanelDataInjection(t *testing.T) {
	s := &Server{}
	s.SetPanelDataFn("memory", func() any {
		return map[string]any{"kind": "list-detail", "items": []map[string]any{{"id": "k", "title": "k"}}}
	})
	rec := httptest.NewRecorder()
	s.handlePanelData(rec, httptest.NewRequest(http.MethodGet, "/api/panel/memory", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("injected memory panel = %d, want 200", rec.Code)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode injected panel: %v", err)
	}
	items, _ := m["items"].([]any)
	if len(items) != 1 {
		t.Errorf("injected items = %d, want 1 (body: %s)", len(items), rec.Body.String())
	}
	// 未注入面维持 501
	rec2 := httptest.NewRecorder()
	s.handlePanelData(rec2, httptest.NewRequest(http.MethodGet, "/api/panel/usage", nil))
	if rec2.Code != http.StatusNotImplemented {
		t.Errorf("non-injected usage panel = %d, want 501", rec2.Code)
	}
}
