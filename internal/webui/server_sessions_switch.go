package webui

import (
	"encoding/json"
	"net/http"
	"strings"
)

// SetSessionSwitchFn 注入会话切换执行面(TUI repl.RequestSessionSwitch——
// 实际切换由后端 TUI/desktop 全链执行,webui 只发起请求,与桌面端同逻辑)
func (s *Server) SetSessionSwitchFn(fn func(sessionID string) error) {
	s.sessionSwitchFn = fn
}

// handleSessionSwitch POST /api/sessions/switch {"id": "..."}
// 锁定语义:本实例 busy(agent running)→ 409;目标会话被他实例锁 → resume 链内层拒绝
func (s *Server) handleSessionSwitch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.sessionSwitchFn == nil {
		writeError(w, http.StatusServiceUnavailable, "session switch unavailable in this mode")
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.ID) == "" {
		writeError(w, http.StatusBadRequest, "missing session id")
		return
	}
	if err := s.sessionSwitchFn(req.ID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "switching", "id": req.ID})
}
