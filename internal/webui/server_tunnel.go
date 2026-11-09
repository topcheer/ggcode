package webui

import (
	"net/http"
)

// SetTunnelStatusFn 注入双隧道状态快照源(repl.TunnelStatusSnapshot——
// share=mobile relay /share 家族;et=公网 WebUI 隧道 /et 家族)。
func (s *Server) SetTunnelStatusFn(fn func() map[string]any) {
	s.mu.Lock()
	s.tunnelStatusFn = fn
	s.mu.Unlock()
}

// handleTunnelStatus GET /api/tunnel/status —— 双隧道只读状态。
// 启动/停止不走本端点:前端经 POST /api/chat/commands 发 "/share"/"/et on"/
// "/et off" 等 slash 命令(复用 TUI 全链校验/busy 语义/反馈消息回流)。
func (s *Server) handleTunnelStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	fn := s.tunnelStatusFn
	s.mu.RUnlock()
	if fn == nil {
		writeJSON(w, map[string]any{
			"share": map[string]any{"active": false, "starting": false, "connected": false},
			"et":    map[string]any{"active": false, "url": ""},
		})
		return
	}
	writeJSON(w, fn())
}
