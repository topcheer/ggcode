package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/notify"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/safego"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
)

func (m Model) handleApprovalMsg(msg ApprovalMsg) (Model, tea.Cmd) {

	if m.pendingApproval != nil && m.pendingApproval.Response != nil {
		stale := *m.pendingApproval

		if oldID := m.tunnelPendingApprovalID; oldID != "" {
			m.pushTunnelApprovalResult(oldID, "deny")
			m.tunnelPendingApprovalID = ""
		}
		safego.Go("tui.model.displaceApproval", func() {

			stale.Response <- permission.Cancelled
		})
	}

	m.pendingApproval = &msg

	m.approvalOptions = defaultApprovalOptionsFor(m.currentLanguage())
	m.approvalCursor = 0
	m.inputBellFired = false
	m.oscBlocked(oscKindPermission, "Approval needed: "+msg.ToolName)

	m.approvalNotifiedIM = false
	if m.imEmitter != nil {
		m.emitIMApproval(msg.ToolName, msg.Input)
		m.approvalNotifiedIM = true
	}

	if broker := m.tunnelEventBroker(); broker != nil {
		m.tunnelPendingApprovalID = m.nextTunnelRequestID()
		agentruntime.PushTunnelApprovalRequest(broker, m.tunnelPendingApprovalID, msg.ToolName, msg.Input, agentruntime.TunnelStateUpdate{
			HasStatus: true,
			Status:    tunnel.StatusBusy,
		})
	}

	return m, m.scheduleInputBell("Approval needed: " + msg.ToolName)

}

func (m Model) handleDiffConfirmMsg(msg DiffConfirmMsg) (Model, tea.Cmd) {

	m.pendingDiffConfirm = &msg

	m.diffOptions = diffConfirmOptionsFor(m.currentLanguage())
	m.diffCursor = 0
	m.inputBellFired = false
	m.oscBlocked(oscKindPermission, "Confirm edit")

	return m, m.scheduleInputBell("Diff confirmation needed")

}

func (m Model) handleAskUserMsg(msg AskUserMsg) (Model, tea.Cmd) {
	if m.pendingQuestionnaire != nil {
		if msg.Response != nil {
			safego.Go("tui.model.cancelAskUser", func() {
				msg.Response <- toolpkg.AskUserResponse{
					Status:        toolpkg.AskUserStatusCancelled,
					Title:         msg.Request.Title,
					QuestionCount: len(msg.Request.Questions),
				}
			})
		}
		return m, nil
	}
	m.pendingQuestionnaire = newQuestionnaireState(msg.Request, msg.Response, m.currentLanguage())
	m.syncQuestionnaireInputWidth()
	m.inputBellFired = false
	m.oscBlocked(oscKindQuestion, "Question: "+msg.Request.Title)

	if len(msg.Request.Questions) > 0 {
		q := msg.Request.Questions[0]
		fallback := m.formatIMAskUserQuestion(msg.Request.Title, q)
		if len(q.Choices) > 0 {
			m.emitIMAskUserInteractive(msg.Request.Title, q, fallback)
		} else {
			m.emitIMAskUser(fallback)
		}
	}

	if broker := m.tunnelEventBroker(); broker != nil {
		m.tunnelPendingAskUserID = m.nextTunnelRequestID()
		agentruntime.PushTunnelAskUserRequest(broker, m.tunnelPendingAskUserID, msg.Request, agentruntime.TunnelStateUpdate{
			HasStatus: true,
			Status:    tunnel.StatusBusy,
		})
	}

	return m, m.scheduleInputBell("Question: " + msg.Request.Title)

}

func (m Model) scheduleInputBell(summary string) tea.Cmd {
	var delaySec int
	if m.config != nil {
		delaySec = m.config.Notifications.EffectiveInputBellDelay()
	} else {
		delaySec = config.NotificationConfig{}.EffectiveInputBellDelay()
	}
	if delaySec <= 0 {
		return nil
	}
	delay := time.Duration(delaySec) * time.Second
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return inputBellMsg{summary: summary}
	})
}

func (m Model) handleInputBellMsg(msg inputBellMsg) (Model, tea.Cmd) {

	if m.inputBellFired {
		return m, nil
	}
	if m.pendingApproval == nil && m.pendingDiffConfirm == nil &&
		m.pendingQuestionnaire == nil {
		return m, nil
	}
	m.inputBellFired = true
	var cfg config.NotificationConfig
	if m.config != nil {
		cfg = m.config.Notifications
	}
	notify.OnInputNeeded(cfg, msg.summary)
	return m, nil
}
