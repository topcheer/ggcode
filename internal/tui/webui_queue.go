package tui

// webui_queue.go:插入消息队列的 webui 通知面。
// TUI 排队全链(queuePendingSubmissionOrigin/dequeueLastVisible/
// submitPendingSubmissionCmd/clearPendingSubmissions)在三时机回调 queueNotifyFn,
// webui 端镜像 append/dequeue/clear 事件——语义零复制,队列仍在 TUI。

// SetQueueNotifyFn 注入队列事件回调(启动时由 root 接线,非并发写)
func (r *REPL) SetQueueNotifyFn(fn func(event, text string, count int)) {
	r.model.queueNotifyFn = fn
}

// QueuedSubmissions 返回可见排队项文本快照(webui GET /api/chat/queue)
func (r *REPL) QueuedSubmissions() []string {
	r.currentSessionMu.RLock()
	defer r.currentSessionMu.RUnlock()
	return r.model.QueuedSubmissions()
}
