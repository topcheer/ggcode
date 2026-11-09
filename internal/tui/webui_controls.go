package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
)

// commandRequestMsg 让外部(webui 聊天面板三能力:切模型/选 effort/切 mode)
// 复用 TUI slash 命令全链——校验、反馈系统消息、busy 语义、配置保存全部
// 由 handleCommandWithDisplay 继承,webui 不复制任何语义。
type commandRequestMsg struct {
	text string
}

func (m *Model) handleCommandRequest(msg commandRequestMsg) (Model, tea.Cmd) {
	debug.Log("tui", "commandRequest: %s", msg.text)
	cmd := m.handleCommandWithDisplay(msg.text, false)
	return *m, cmd
}

// SubmitCommand 供外部(webui bridge)提交 slash 命令文本。
// busy 校验交给命令自身语义(/model 等在 busy 期合法,/resume 内层拒绝)。
func (r *REPL) SubmitCommand(text string) error {
	if text == "" {
		return fmt.Errorf("empty command")
	}
	r.sendProgramMsgs(commandRequestMsg{text: text})
	return nil
}

// ChatControlsSnapshot 返回聊天面板三能力当前值(模型/权限模式/推理力度)。
// 只读快照,与 RequestSessionSwitch 同锁面。
// 真值源纪律:r.model 是 bubbletea 值语义的启动拷贝,运行期 /mode 经
// ConfigPolicy 指针切换不会回写它的 m.mode 字段——mode 必须读 policy,
// reasoning_effort 必须读 REPL 直达的 r.agent(旧拷贝 m.agent 恒 nil)。
func (r *REPL) ChatControlsSnapshot() map[string]string {
	r.currentSessionMu.RLock()
	defer r.currentSessionMu.RUnlock()
	m := r.model
	snap := map[string]string{}
	mode := m.mode
	if cp, ok := m.policy.(*permission.ConfigPolicy); ok {
		mode = cp.CurrentMode()
	}
	snap["mode"] = mode.String()
	if r.agent != nil {
		snap["reasoning_effort"] = r.agent.ReasoningEffort()
	} else if m.agent != nil {
		snap["reasoning_effort"] = m.agent.ReasoningEffort()
	}
	if m.config != nil {
		if resolved, err := m.config.ResolveActiveEndpoint(); err == nil {
			snap["model"] = resolved.Model
		}
	}
	return snap
}
