package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/safego"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
)

// handleApprovalMsg handles the corresponding message case.
func (m Model) handleApprovalMsg(msg ApprovalMsg) (Model, tea.Cmd) {
	if m.mode == permission.AutopilotMode {
		m.pendingApproval = &msg
		return m, m.handleApproval(permission.Allow)
	}
	// Agent is requesting approval
	m.pendingApproval = &msg
	m.approvalOptions = defaultApprovalOptions()
	m.approvalCursor = 0
	// Push to IM if available so user can approve remotely
	m.approvalNotifiedIM = false
	if m.imEmitter != nil {
		m.emitIMApproval(msg.ToolName, msg.Input)
		m.approvalNotifiedIM = true
	}
	// Push to mobile tunnel client
	if broker := m.tunnelEventBroker(); broker != nil {
		m.tunnelPendingApprovalID = m.nextTunnelRequestID()
		agentruntime.PushTunnelApprovalRequest(broker, m.tunnelPendingApprovalID, msg.ToolName, msg.Input, agentruntime.TunnelStateUpdate{
			HasStatus: true,
			Status:    tunnel.StatusBusy,
		})
	}
	return m, nil

}

// handleDiffConfirmMsg handles the corresponding message case.
func (m Model) handleDiffConfirmMsg(msg DiffConfirmMsg) (Model, tea.Cmd) {
	if m.mode == permission.AutopilotMode {
		m.pendingDiffConfirm = &msg
		return m, m.handleDiffConfirm(true)
	}
	m.pendingDiffConfirm = &msg
	m.diffOptions = diffConfirmOptions()
	m.diffCursor = 0
	return m, nil

}

// handleAskUserMsg handles the corresponding message case.
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
	// Push the first question to IM so remote users can answer.
	if len(msg.Request.Questions) > 0 {
		q := msg.Request.Questions[0]
		fallback := m.formatIMAskUserQuestion(msg.Request.Title, q)
		if len(q.Choices) > 0 {
			m.emitIMAskUserInteractive(msg.Request.Title, q, fallback)
		} else {
			m.emitIMAskUser(fallback)
		}
	}
	// Push to mobile tunnel client
	if broker := m.tunnelEventBroker(); broker != nil {
		m.tunnelPendingAskUserID = m.nextTunnelRequestID()
		agentruntime.PushTunnelAskUserRequest(broker, m.tunnelPendingAskUserID, msg.Request, agentruntime.TunnelStateUpdate{
			HasStatus: true,
			Status:    tunnel.StatusBusy,
		})
	}
	return m, nil

}

// handleHarnessCheckpointConfirmMsg handles the corresponding message case.
func (m Model) handleHarnessCheckpointConfirmMsg(msg HarnessCheckpointConfirmMsg) (Model, tea.Cmd) {
	if m.mode == permission.AutopilotMode {
		m.pendingHarnessCheckpointConfirm = &msg
		return m, m.handleHarnessCheckpointConfirm(true)
	}
	m.pendingHarnessCheckpointConfirm = &msg
	m.diffOptions = diffConfirmOptions()
	m.diffCursor = 0
	return m, nil

}
