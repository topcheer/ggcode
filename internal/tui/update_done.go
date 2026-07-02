package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"

	"github.com/topcheer/ggcode/internal/tunnel"
)

// handleDoneMsg handles the corresponding message case.
func (m Model) handleDoneMsg(msg doneMsg) (Model, tea.Cmd) {
	finalIMText := m.pendingIMStreamText()
	m.setLoading(false)
	m.remoteInboundAdapter = "" // reset per-channel suppression after agent run
	// Notify LAN Chat peers that our agent is now idle
	if m.lanChatHub != nil {
		m.lanChatHub.SetAgentBusy(false)
	}
	m.spinner.Stop()
	m.chatFinishAllRunningTools()
	m.cancelFunc = nil
	m.streamPrefixWritten = false
	// Finalize streaming assistant in chatList
	m.chatFinishAssistant(m.currentAssistantID())
	wasCanceled := m.runCanceled
	wasFailed := m.runFailed
	m.runCanceled = false
	m.runFailed = false
	m.statusActivity = ""
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	m.rolloverTunnelMainStream(false)
	m.pushTunnelCurrentStatus()
	m.pushTunnelCurrentActivity()
	if m.streamBuffer != nil && m.streamBuffer.Len() > 0 {
		m.renderStreamBuffer(true)
		m.streamBuffer = nil
	}
	if finalIMText != "" {
		m.emitIMText(finalIMText)
	}
	m.chatListScrollToBottom()
	// Only persist here for normal completion. For cancel/error paths,
	// persistFullSessionMessages was already called by handleErrMsg or
	// handleAgentErrMsg. Calling it again would duplicate all records.
	if !wasCanceled && !wasFailed {
		m.persistFullSessionMessages()
	}
	if !wasCanceled && !wasFailed && m.pendingSubmissionCount() > 0 {
		return m, m.submitPendingSubmissionCmd()
	}
	return m, nil

}

// handleAgentDoneMsg handles the corresponding message case.
func (m Model) handleAgentDoneMsg(msg agentDoneMsg) (Model, tea.Cmd) {
	if msg.RunID != m.activeAgentRunID {
		return m, nil
	}
	// Send "completed" receipt for lanchat messages that triggered this agent run
	if m.lanChatPendingComplete != "" && m.lanChatHub != nil {
		m.lanChatHub.NotifyAgentComplete(m.lanChatPendingComplete)
		m.lanChatPendingComplete = ""
	}
	if m.agent != nil {
		m.projMemFiles = m.agent.ProjectMemoryFiles()
	}
	m.setLoading(false)
	m.remoteInboundAdapter = "" // reset per-channel suppression
	// Notify LAN Chat peers that our agent is now idle
	if m.lanChatHub != nil {
		m.lanChatHub.SetAgentBusy(false)
	}
	m.spinner.Stop()
	m.chatFinishAllRunningTools()
	m.cancelFunc = nil
	m.chatFinishAssistant(m.currentAssistantID())
	wasCanceled := m.runCanceled
	wasFailed := m.runFailed
	m.runCanceled = false
	m.runFailed = false
	m.statusActivity = ""
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	m.rolloverTunnelMainStream(false)
	m.pushTunnelCurrentStatus()
	m.pushTunnelCurrentActivity()
	if m.streamBuffer != nil && m.streamBuffer.Len() > 0 {
		m.renderStreamBuffer(true)
		m.streamBuffer = nil
	}
	if !wasCanceled && !wasFailed {
		m.appendTurnMetricsDigest(m.usageTurnIndex)
	}
	m.chatListScrollToBottom()
	if !wasCanceled && !wasFailed {
		m.persistFullSessionMessages()
	}
	if !wasCanceled && !wasFailed && m.pendingSubmissionCount() > 0 {
		return m, m.submitPendingSubmissionCmd()
	}
	return m, nil

}

// handleErrMsg handles the corresponding message case.
func (m Model) handleErrMsg(msg errMsg) (Model, tea.Cmd) {
	if errors.Is(msg.err, context.Canceled) {
		return m, nil
	}
	m.runFailed = true
	m.setLoading(false)
	m.spinner.Stop()
	m.chatFinishAllRunningTools()
	m.cancelFunc = nil
	m.statusActivity = ""
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	m.rolloverTunnelMainStream(false)
	if m.pendingSubmissionCount() > 0 {
		m.restorePendingInput()
	}
	m.pushTunnelStatus(tunnel.StatusIdle, "")
	m.pushTunnelCurrentActivity()
	m.chatWriteSystem(nextSystemID(), formatUserFacingError(m.currentLanguage(), msg.err))
	m.chatListScrollToBottom()
	m.persistFullSessionMessages()
	return m, nil

}

// handleAgentErrMsg handles the corresponding message case.
func (m Model) handleAgentErrMsg(msg agentErrMsg) (Model, tea.Cmd) {
	if msg.RunID != m.activeAgentRunID {
		return m, nil
	}
	if errors.Is(msg.Err, context.Canceled) {
		// Even on cancellation, persist any messages that were added
		// before the cancel (e.g. partial assistant response, tool results).
		m.persistFullSessionMessages()
		return m, nil
	}
	m.runFailed = true
	m.setLoading(false)
	m.spinner.Stop()
	m.chatFinishAllRunningTools()
	m.cancelFunc = nil
	m.statusActivity = ""
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	m.rolloverTunnelMainStream(false)
	if m.pendingSubmissionCount() > 0 {
		m.restorePendingInput()
	}
	m.pushTunnelStatus(tunnel.StatusIdle, "")
	m.pushTunnelCurrentActivity()
	m.chatWriteSystem(nextSystemID(), formatUserFacingError(m.currentLanguage(), msg.Err))
	m.emitIMText(formatUserFacingError(m.currentLanguage(), msg.Err))
	m.chatListScrollToBottom()
	m.persistFullSessionMessages()
	return m, nil

}
