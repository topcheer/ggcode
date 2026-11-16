package webui

import (
	"encoding/json"
	"net/http"
	"strings"
)

// SetChatCommandFn 注入 slash 命令提交面(REPL.SubmitCommand——webui 聊天面板
// 三能力切模型/选 effort/切 mode 全部复用 TUI 命令全链,语义零复制)
func (s *Server) SetChatCommandFn(fn func(text string) error) {
	s.chatCommandFn = fn
}

// SetChatControlsFn 注入三能力当前值快照面(REPL.ChatControlsSnapshot)
func (s *Server) SetChatControlsFn(fn func() map[string]string) {
	s.chatControlsFn = fn
}

// handleChatControls GET /api/chat/controls → {model, mode, reasoning_effort}
func (s *Server) handleChatControls(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.chatControlsFn == nil {
		writeJSON(w, map[string]string{})
		return
	}
	writeJSON(w, s.chatControlsFn())
}

// handleChatCommand POST /api/chat/commands {"text": "/model x" | "/mode bypass" | "/effort"}
// 语义与 TUI 输入框完全一致(含反馈系统消息,经 WS text 通道回流前端)
func (s *Server) handleChatCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.chatCommandFn == nil {
		writeError(w, http.StatusServiceUnavailable, "commands unavailable in this mode")
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Text) == "" {
		writeError(w, http.StatusBadRequest, "missing command text")
		return
	}
	if !strings.HasPrefix(req.Text, "/") {
		writeError(w, http.StatusBadRequest, "not a slash command")
		return
	}
	// 面板类命令(无参形态):不送 TUI(那会在 TUI 屏弹面板,webui 用户
	// 不可见),返回面板指令让前端渲染 webui 面板;选择回填为带参命令
	// 二次提交走下方直通链。带参命令永不进此分支。
	if spec, base, ok := panelDirectiveFor(req.Text); ok {
		writeJSON(w, map[string]interface{}{
			"status":    "panel",
			"command":   "/" + base,
			"kind":      spec.Kind,
			"title":     spec.Title,
			"dataUrl":   spec.DataURL,
			"cmdPrefix": spec.CmdPrefix,
		})
		return
	}
	if err := s.chatCommandFn(req.Text); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "submitted", "text": req.Text})
}
