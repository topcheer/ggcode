package tui

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/notify"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/tunnel"
)

func (m Model) notificationSummary(duration time.Duration, toolCount int) string {

	if toolCount > 0 {
		return fmt.Sprintf("Agent completed after %s (%d tool calls).", duration.Round(time.Second), toolCount)
	}
	return fmt.Sprintf("Agent completed after %s.", duration.Round(time.Second))
}

func (m Model) handleDoneMsg(msg doneMsg) (Model, tea.Cmd) {
	finalIMText := m.pendingIMStreamText()
	m.setLoading(false)
	m.remoteInboundAdapter = ""

	if m.lanChatHub != nil {
		m.lanChatHub.SetAgentBusy(false)
	}
	m.spinner.Stop()
	m.chatFinishAllRunningTools()
	m.cancelFunc = nil
	m.streamPrefixWritten = false

	m.chatFinishAssistant(m.currentAssistantID())
	wasCanceled := m.runCanceled
	wasFailed := m.runFailed
	m.runCanceled = false
	m.runFailed = false
	m.oscRunOutcome(wasFailed, wasCanceled)

	if m.lanChatPendingComplete != "" && m.lanChatHub != nil {
		if wasCanceled || wasFailed {
			m.lanChatHub.NotifyAgentNotCompleted(m.lanChatPendingComplete)
		} else {
			m.lanChatHub.NotifyAgentComplete(m.lanChatPendingComplete)
		}
		m.lanChatPendingComplete = ""
	}
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
	m.chatListFollowOutput()

	if !wasCanceled && !wasFailed {
		m.persistFullSessionMessages()
	}

	if m.pendingRestart {
		debug.Log("restart", "turn finished (doneMsg): firing armed restart")
		if m.pendingSubmissionCount() > 0 {
			m.restorePendingInput()
		}
		return m, firePendingRestartCmd()
	}
	if !wasCanceled && !wasFailed && m.pendingSubmissionCount() > 0 {
		return m, m.submitPendingSubmissionCmd()
	}
	return m, nil

}

func (m Model) handleAgentDoneMsg(msg agentDoneMsg) (Model, tea.Cmd) {
	if msg.RunID != m.activeAgentRunID {
		return m, nil
	}
	if m.agent != nil {
		m.projMemFiles = m.agent.ProjectMemoryFiles()
	}
	m.setLoading(false)
	(&m).refreshStatusline()
	m.remoteInboundAdapter = ""

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
	m.oscRunOutcome(wasFailed, wasCanceled)

	if m.lanChatPendingComplete != "" && m.lanChatHub != nil {
		if wasCanceled || wasFailed {
			m.lanChatHub.NotifyAgentNotCompleted(m.lanChatPendingComplete)
		} else {
			m.lanChatHub.NotifyAgentComplete(m.lanChatPendingComplete)
		}
		m.lanChatPendingComplete = ""
	}
	m.statusActivity = ""
	m.statusToolName = ""
	m.statusToolArg = ""

	doneToolCount := m.statusToolCount
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
		m.appendRunChangeSummary()
	}
	m.chatListFollowOutput()

	notifCfg := config.NotificationConfig{}
	if m.config != nil {
		notifCfg = m.config.Notifications
	}
	var runDur time.Duration
	if !m.runStartTime.IsZero() {
		runDur = time.Since(m.runStartTime)
	}
	summary := m.notificationSummary(runDur, doneToolCount)
	notify.OnCompletion(notifCfg, runDur, wasFailed, summary)
	if !wasCanceled && !wasFailed {
		m.persistFullSessionMessages()
		m.maybeRefineSessionTitle(doneToolCount)
	}

	if m.pendingRestart {
		debug.Log("restart", "turn finished (agentDoneMsg): firing armed restart")
		if m.pendingSubmissionCount() > 0 {
			m.restorePendingInput()
		}
		return m, firePendingRestartCmd()
	}
	if !wasCanceled && !wasFailed {

		m.resetBlindSpotRetry()
		if m.pendingSubmissionCount() > 0 {
			return m, m.submitPendingSubmissionCmd()
		}
	}
	return m, nil

}

func (m Model) handleErrMsg(msg errMsg) (Model, tea.Cmd) {
	if errors.Is(msg.err, context.Canceled) {

		m.remoteInboundAdapter = ""
		m.chatFinishAssistant(m.currentAssistantID())
		if m.streamBuffer != nil {
			m.renderStreamBuffer(true)
			m.streamBuffer = nil
		}

		m.persistFullSessionMessages()
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
		m.chatListFollowOutput()
		return m, nil
	}

	m.remoteInboundAdapter = ""
	m.chatFinishAssistant(m.currentAssistantID())
	if m.streamBuffer != nil {
		m.renderStreamBuffer(true)
		m.streamBuffer = nil
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

	retryCmd := m.maybeBlindSpotRetry(msg.err)
	m.chatListFollowOutput()

	notifCfg := config.NotificationConfig{}
	if m.config != nil {
		notifCfg = m.config.Notifications
	}
	var runDur time.Duration
	if !m.runStartTime.IsZero() {
		runDur = time.Since(m.runStartTime)
	}
	notify.OnCompletion(notifCfg, runDur, true, "Agent run failed with an error.")
	m.persistFullSessionMessages()
	return m, retryCmd

}

func (m Model) handleAgentErrMsg(msg agentErrMsg) (Model, tea.Cmd) {
	if msg.RunID != m.activeAgentRunID {
		return m, nil
	}
	if errors.Is(msg.Err, context.Canceled) {

		m.remoteInboundAdapter = ""
		m.chatFinishAssistant(m.currentAssistantID())
		if m.streamBuffer != nil {
			m.renderStreamBuffer(true)
			m.streamBuffer = nil
		}

		m.persistFullSessionMessages()
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
		m.chatListFollowOutput()
		return m, nil
	}

	m.remoteInboundAdapter = ""
	m.chatFinishAssistant(m.currentAssistantID())
	if m.streamBuffer != nil {
		m.renderStreamBuffer(true)
		m.streamBuffer = nil
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

	retryCmd := m.maybeBlindSpotRetry(msg.Err)
	m.chatListFollowOutput()

	notifCfg := config.NotificationConfig{}
	if m.config != nil {
		notifCfg = m.config.Notifications
	}
	var runDur time.Duration
	if !m.runStartTime.IsZero() {
		runDur = time.Since(m.runStartTime)
	}
	notify.OnCompletion(notifCfg, runDur, true, "Agent run failed with an error.")
	m.persistFullSessionMessages()
	return m, retryCmd

}

func (m Model) maybeRefineSessionTitle(toolCount int) {
	if m.session == nil || m.sessionStore == nil {
		return
	}

	agentTurnCount := 0
	for _, msg := range m.session.Messages {
		if msg.Role == "assistant" {
			agentTurnCount++
		}
	}
	if agentTurnCount > 2 {
		return
	}

	var firstUserMsg string
	for _, msg := range m.session.Messages {
		if msg.Role == "user" {
			for _, block := range msg.Content {
				if block.Type == "text" && block.Text != "" {
					firstUserMsg = block.Text
					break
				}
			}
			if firstUserMsg != "" {
				break
			}
		}
	}
	if firstUserMsg == "" {
		return
	}

	agentSummary := m.buildAgentTitleSummary(toolCount)

	newTitle := agentruntime.RefineTitleAfterRun(m.session.Title, firstUserMsg, agentSummary)
	if newTitle == "" || newTitle == m.session.Title {
		return
	}

	oldTitle := m.session.Title
	m.session.Title = newTitle
	m.session.UpdatedAt = time.Now()

	ses := m.session
	store := m.sessionStore
	safego.Go("tui.appendMetaToDisk", func() {
		_ = store.AppendMetaToDisk(ses)
	})

	debug.Log("tui", "refined session title: %q -> %q", oldTitle, newTitle)
}

func (m Model) buildAgentTitleSummary(toolCount int) string {
	if toolCount > 0 {
		return fmt.Sprintf("Agent task (%d tool calls)", toolCount)
	}
	return ""
}
