package webui

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// queueEventSendTimeout bounds how long a queue event waits for ws buffer space.
const queueEventSendTimeout = 5 * time.Second

// queueSeq orders queue events per process so a tab can drop stale snapshots
// that were delivered out of order by the per-sink goroutines below.
var queueSeq atomic.Int64

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
// 每个事件都附带当前全量 items 快照与递增 seq:前端以快照为准,事件只是触发;
// 丢一个事件不再造成队列区永久陈旧(旧实现写泵满即丢,dequeue 丢失后横幅不消失)。
// 投递在独立 goroutine 里带超时等待缓冲区,不阻塞 TUI 的 Update 循环。
func (s *Server) EmitQueueEvent(event, text string, count int) {
	s.wsMu.Lock()
	sinks := make([]chan interface{}, 0, len(s.queueSinks))
	for ch := range s.queueSinks {
		sinks = append(sinks, ch)
	}
	snapshotFn := s.queueSnapshotFn
	s.wsMu.Unlock()

	msg := queueMsg(event, text, count, snapshotFn)
	for _, ch := range sinks {
		ch := ch
		safego.Go("webui.queueEvent", func() {
			select {
			case ch <- msg:
			case <-time.After(queueEventSendTimeout):
				debug.Log("webui", "ws write channel full on queue event %q, dropped after 5s (snapshot on next event/reconnect heals)", event)
			}
		})
	}
}

// queueMsg builds a queue message: event + text + count + full items snapshot + seq.
// count is the visible snapshot length (the TUI's count includes hidden internal
// entries the browser never shows); the caller's count is only used without a snapshot.
func queueMsg(event, text string, count int, snapshotFn func() []string) map[string]interface{} {
	items := []string{}
	if snapshotFn != nil {
		if got := snapshotFn(); got != nil {
			items = got
		}
		count = len(items)
	}
	return map[string]interface{}{
		"type": "queue", "event": event, "text": text, "count": count,
		"items": items, "seq": queueSeq.Add(1),
	}
}

// queueSnapshotMsg 连接建立时的全量快照消息
func (s *Server) queueSnapshotMsg() map[string]interface{} {
	s.wsMu.Lock()
	fn := s.queueSnapshotFn
	s.wsMu.Unlock()
	return queueMsg("list", "", 0, fn)
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
