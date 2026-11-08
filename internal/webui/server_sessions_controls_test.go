package webui

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 钉测(会话切换):注入面缺省 503、busy 409、成功 200、非 POST 405、缺 id 400
func TestHandleSessionSwitchBranches(t *testing.T) {
	s := NewServer(nil) // fn 未注入
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/sessions/switch", strings.NewReader(body))
		w := httptest.NewRecorder()
		s.handleSessionSwitch(w, req)
		return w
	}

	if w := post(`{"id":"x"}`); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("no fn: want 503, got %d", w.Code)
	}

	s.SetSessionSwitchFn(func(id string) error { return errors.New("agent busy") })
	if w := post(`{"id":"x"}`); w.Code != http.StatusConflict {
		t.Fatalf("busy: want 409, got %d", w.Code)
	}

	s.SetSessionSwitchFn(func(id string) error {
		if id != "sess-1" {
			t.Fatalf("got id %q", id)
		}
		return nil
	})
	if w := post(`{"id":"sess-1"}`); w.Code != http.StatusOK {
		t.Fatalf("ok: want 200, got %d", w.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/switch", nil)
	w := httptest.NewRecorder()
	s.handleSessionSwitch(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: want 405, got %d", w.Code)
	}
	if w := post(`{}`); w.Code != http.StatusBadRequest {
		t.Fatalf("missing id: want 400, got %d", w.Code)
	}
}

// 钉测(聊天三能力):controls 快照回传 + command 非 slash 拒绝
func TestHandleChatControlsAndCommand(t *testing.T) {
	s := NewServer(nil)
	s.SetChatControlsFn(func() map[string]string {
		return map[string]string{"model": "glm-5.3", "mode": "supervised", "reasoning_effort": "medium"}
	})
	req := httptest.NewRequest(http.MethodGet, "/api/chat/controls", nil)
	w := httptest.NewRecorder()
	s.handleChatControls(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "glm-5.3") {
		t.Fatalf("controls: %d %s", w.Code, w.Body.String())
	}

	allowed := false
	s.SetChatCommandFn(func(text string) error {
		allowed = text == "/mode bypass"
		return nil
	})
	postCmd := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/chat/commands", strings.NewReader(body))
		rec := httptest.NewRecorder()
		s.handleChatCommand(rec, r)
		return rec
	}
	if w := postCmd(`{"text":"hello"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("non-slash: want 400, got %d", w.Code)
	}
	if w := postCmd(`{"text":"/mode bypass"}`); w.Code != http.StatusOK || !allowed {
		t.Fatalf("slash submit: %d allowed=%v", w.Code, allowed)
	}
}
