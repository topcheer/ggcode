package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/debug"
)

// resumeRequestMsg 请求切换到指定会话(webui POST /api/sessions/switch 入口,
// 经 REPL.sendProgramMsgs 注入主循环——实际切换仍由 TUI 的 resume 全链执行:
// 锁检查/pendingResume 去重/applyResumedSession 一路复用,与桌面端同逻辑)
type resumeRequestMsg struct {
	id string
}

func (m *Model) handleResumeRequest(msg resumeRequestMsg) (Model, tea.Cmd) {
	if m.loading {
		debug.Log("tui", "resumeRequest: switch blocked, agent busy")
		return *m, nil // busy=锁定:静默拒绝,webui handler 侧已前置 busy 校验
	}
	return *m, m.resumeSession(msg.id)
}

// RequestSessionSwitch 供外部(webui bridge)请求切换会话。
// busy(loading)即锁定态——返回错误由调用方转 HTTP 409。
// loading 状态经 currentSessionMu 保护区域读取快照(repl 无独立 modelMu;
// loading 在 model 消息循环内变更,竞态由 resume 链内层 switch_blocked_running 兜底)
func (r *REPL) RequestSessionSwitch(sessionID string) error {
	r.currentSessionMu.RLock()
	busy := r.model.loading
	r.currentSessionMu.RUnlock()
	if busy {
		return fmt.Errorf("agent busy")
	}
	r.sendProgramMsgs(resumeRequestMsg{id: sessionID})
	return nil
}
