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

// handleApprovalMsg handles the corresponding message case.
func (m Model) handleApprovalMsg(msg ApprovalMsg) (Model, tea.Cmd) {
	// #1395-A: a second approval (main agent + ACP external agents run
	// concurrently, repl.go wires both) used to overwrite pendingApproval
	// unconditionally - the first request's Response channel was lost (its
	// agent goroutine blocked until ctx timeout -> Deny) and the tunnel
	// approval ID was reassigned (mobile users' answer to the OLD id was
	// silently filtered). Mirror handleAskUserMsg: deny the pending one,
	// register the newcomer.
	if m.pendingApproval != nil && m.pendingApproval.Response != nil {
		stale := *m.pendingApproval
		// #1423-B: the displacement branch denied the stale approval but
		// left m.tunnelPendingApprovalID pointing at it - the autopilot
		// branch below then allowed the NEW request while the push path
		// (commands_slash) read the RESIDUAL old ID: mobile saw the old
		// request 'allowed' (it was denied) and the new result mispaired.
		// Push an explicit deny for the stale ID, then clear it.
		if oldID := m.tunnelPendingApprovalID; oldID != "" {
			m.pushTunnelApprovalResult(oldID, "deny")
			m.tunnelPendingApprovalID = ""
		}
		safego.Go("tui.model.displaceApproval", func() {
			stale.Response <- permission.Deny
		})
	}
	if m.mode == permission.AutopilotMode {
		m.pendingApproval = &msg
		return m, m.handleApproval(permission.Allow)
	}
	// Agent is requesting approval
	m.pendingApproval = &msg
	m.approvalOptions = defaultApprovalOptions()
	m.approvalCursor = 0
	m.inputBellFired = false
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
	// Schedule delayed input bell so users who switched windows get notified
	return m, m.scheduleInputBell("Approval needed: " + msg.ToolName)

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
	m.inputBellFired = false
	// Schedule delayed input bell
	return m, m.scheduleInputBell("Diff confirmation needed")

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
	m.inputBellFired = false
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
	// Schedule delayed input bell
	return m, m.scheduleInputBell("Question: " + msg.Request.Title)

}

// scheduleInputBell returns a tea.Cmd that fires an inputBellMsg after the
// configured delay. If the delay is zero or the feature is disabled, it
// returns nil (no notification will fire).
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

// handleInputBellMsg fires the delayed notification if the user hasn't
// responded yet. It's idempotent — if the input was already handled, the
// pending fields will be nil and nothing fires.
func (m Model) handleInputBellMsg(msg inputBellMsg) (Model, tea.Cmd) {
	// Only fire if something is still pending and we haven't already fired
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
