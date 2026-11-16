package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPanelDirectiveFor 钉面板命令分流判定表:面板类无参→面板指令;
// 带参→直通(nil);非面板命令→直通。
func TestPanelDirectiveFor(t *testing.T) {
	cases := []struct {
		text   string
		wantOK bool
		base   string
	}{
		{"/model", true, "model"},
		{"/model glm-5.3", false, ""},   // 带参直通命令链
		{"/qq", true, "qq"},             // NoArg 面板
		{"/sessions", true, "sessions"}, // NoArg
		{"/resume abc123", false, ""},   // /resume 带参=直通 resumeSession
		{"/help", false, ""},            // 非面板文本命令
		{"/effort", true, "effort"},
		{"/effort high", false, ""},
		{"hello", false, ""}, // 非 slash
	}
	for _, c := range cases {
		spec, base, ok := panelDirectiveFor(c.text)
		if ok != c.wantOK {
			t.Errorf("panelDirectiveFor(%q) ok=%v, want %v", c.text, ok, c.wantOK)
			continue
		}
		if ok && (base != c.base || spec.Kind == "") {
			t.Errorf("panelDirectiveFor(%q) base=%q kind=%q, want base=%q with kind", c.text, base, spec.Kind, c.base)
		}
	}
}

// TestHandleChatCommandPanelDirective 钉端点行为:无参面板命令返回
// status=panel 指令且 chatCommandFn 不被调用(TUI 不弹面板)。
func TestHandleChatCommandPanelDirective(t *testing.T) {
	called := false
	s := &Server{chatCommandFn: func(text string) error {
		called = true
		return nil
	}}
	rec := httptest.NewRecorder()
	s.handleChatCommand(rec, httptest.NewRequest(http.MethodPost, "/api/chat/commands", strings.NewReader(`{"text":"/model"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if called {
		t.Error("chatCommandFn called for no-arg panel command; TUI panel must NOT open from webui")
	}
	var resp struct {
		Status  string `json:"status"`
		Kind    string `json:"kind"`
		DataURL string `json:"dataUrl"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "panel" || resp.Kind != "select" || resp.DataURL != "/api/panel/model" {
		t.Errorf("panel directive = %+v, want status=panel kind=select url=/api/panel/model", resp)
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
