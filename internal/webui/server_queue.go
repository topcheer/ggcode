package webui

import (
	"net/http"
)

// SetQueueSnapshotFn 注入队列快照面(REPL.QueuedSubmissions,连接建立时全量同步)
func (s *Server) SetQueueSnapshotFn(fn func() []string) {
	s.wsMu.Lock()
	s.queueSnapshotFn = fn
	s.wsMu.Unlock()
}

// registerQueueSink 注册 WS 连接为队列事件接收端(连接建立时调用,断开时注销)
func (s *Server) registerQueueSink(ch chan interface{}) func() {
	s.wsMu.Lock()
	s.queueSinks[ch] = struct{}{}
	s.wsMu.Unlock()
	return func() {
		s.wsMu.Lock()
		delete(s.queueSinks, ch)
		s.wsMu.Unlock()
	}
}

// EmitQueueEvent TUI 队列事件入口(root 经 repl.SetQueueNotifyFn 注入本方法):
// 广播 append/dequeue/clear 到所有活跃 WS 连接,前端镜像排队区。
// 非阻塞发送(写泵 64 缓冲满则丢弃——队列事件幂等,由快照补齐)
func (s *Server) EmitQueueEvent(event, text string, count int) {
	s.wsMu.Lock()
	sinks := make([]chan interface{}, 0, len(s.queueSinks))
	for ch := range s.queueSinks {
		sinks = append(sinks, ch)
	}
	s.wsMu.Unlock()
	msg := map[string]interface{}{"type": "queue", "event": event, "text": text, "count": count}
	for _, ch := range sinks {
		select {
		case ch <- msg:
		default:
		}
	}
}

// queueSnapshotMsg 连接建立时的全量快照消息
func (s *Server) queueSnapshotMsg() map[string]interface{} {
	s.wsMu.Lock()
	fn := s.queueSnapshotFn
	s.wsMu.Unlock()
	items := []string{}
	if fn != nil {
		if got := fn(); got != nil {
			items = got
		}
	}
	return map[string]interface{}{"type": "queue", "event": "list", "items": items, "count": len(items)}
}

// handleChatQueue GET /api/chat/queue → 可见排队项快照
func (s *Server) handleChatQueue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.wsMu.Lock()
	fn := s.queueSnapshotFn
	s.wsMu.Unlock()
	items := []string{}
	if fn != nil {
		if got := fn(); got != nil {
			items = got
		}
	}
	writeJSON(w, map[string]interface{}{"items": items, "count": len(items)})
}
