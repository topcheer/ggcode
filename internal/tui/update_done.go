package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/notify"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/tunnel"
)

// notificationSummary builds a short human-readable summary of the completed
// agent run for use in desktop notifications.
func (m Model) notificationSummary(duration time.Duration, toolCount int) string {
	// #917: toolCount passed by the caller (captured BEFORE the status
	// reset zeroed m.statusToolCount).
	if toolCount > 0 {
		return fmt.Sprintf("Agent completed after %s (%d tool calls).", duration.Round(time.Second), toolCount)
	}
	return fmt.Sprintf("Agent completed after %s.", duration.Round(time.Second))
}

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
	m.chatListFollowOutput()
	// Only persist here for normal completion. For cancel/error paths,
	// persistFullSessionMessages was already called by handleErrMsg or
	// handleAgentErrMsg. Calling it again would duplicate all records.
	if !wasCanceled && !wasFailed {
		m.persistFullSessionMessages()
	}
	// Fire an armed agent-requested restart now that the turn's tool results
	// and trailing text are persisted (#347). Fires for canceled/failed runs
	// too — persistence already happened on those paths. Queued user input is
	// restored first (mirrors the cancel path's restorePendingInput) so it is
	// not silently dropped by the restart (#362).
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
	// #917: capture the tool count BEFORE zeroing - notificationSummary
	// read it after the reset, making the '(N tool calls)' branch dead.
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
	// Fire configurable notification (bell and/or desktop) based on user
	// preferences. Replaces the previously hardcoded bell-only approach.
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
	// Fire an armed agent-requested restart now that the turn's tool results
	// and trailing text are persisted (#347). Fires for canceled/failed runs
	// too — persistence already happened on those paths. Queued user input is
	// restored first so it is not silently dropped by the restart (#362).
	if m.pendingRestart {
		debug.Log("restart", "turn finished (agentDoneMsg): firing armed restart")
		if m.pendingSubmissionCount() > 0 {
			m.restorePendingInput()
		}
		return m, firePendingRestartCmd()
	}
	if !wasCanceled && !wasFailed {
		// Successful run: restore the full blind-spot auto-retry budget
		// for the next failure.
		m.resetBlindSpotRetry()
		if m.pendingSubmissionCount() > 0 {
			return m, m.submitPendingSubmissionCmd()
		}
	}
	return m, nil

}

// handleErrMsg handles the corresponding message case.
func (m Model) handleErrMsg(msg errMsg) (Model, tea.Cmd) {
	if errors.Is(msg.err, context.Canceled) {
		// Even on cancellation, persist any messages that were added
		// during the run (e.g. partial assistant response, tool results).
		// The agent loop already fills cancelled tool_results via
		// fillCancelledToolResults(), so the context is consistent.
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
	// Blind-spot errors: expose raw cause, enable file logging, and
	// schedule an auto-retry of the same submission. Merged into the final
	// return - an early return would skip notify + persist below.
	retryCmd := m.maybeBlindSpotRetry(msg.err)
	m.chatListFollowOutput()
	// Fire notification for error completion.
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

// handleAgentErrMsg handles the corresponding message case.
func (m Model) handleAgentErrMsg(msg agentErrMsg) (Model, tea.Cmd) {
	if msg.RunID != m.activeAgentRunID {
		return m, nil
	}
	if errors.Is(msg.Err, context.Canceled) {
		// Even on cancellation, persist any messages that were added
		// before the cancel (e.g. partial assistant response, tool results).
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
	// Blind-spot errors: expose raw cause, enable file logging, and
	// schedule an auto-retry of the same submission. Merged into the final
	// return - an early return would skip notify + persist below.
	retryCmd := m.maybeBlindSpotRetry(msg.Err)
	m.chatListFollowOutput()
	// Fire notification for agent error completion.
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

// maybeRefineSessionTitle improves the session title after the first agent run.
// If the initial title was generic (e.g., "hi", "help"), it tries to derive a
// better title from the first user message or the agent's activity summary.
// User-set titles (via /title) are never overridden.
func (m Model) maybeRefineSessionTitle(toolCount int) {
	if m.session == nil || m.sessionStore == nil {
		return
	}

	// Only refine on the first or second run (when titles are most uncertain).
	agentTurnCount := 0
	for _, msg := range m.session.Messages {
		if msg.Role == "assistant" {
			agentTurnCount++
		}
	}
	if agentTurnCount > 2 {
		return
	}

	// Get the first user message for title extraction.
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

	// Build a brief summary of what the agent did.
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

// buildAgentTitleSummary creates a brief description of the agent's activity
// for use as a fallback title when the user message is too generic.
// #1013: takes the captured pre-reset tool count - reading m.statusToolCount
// here was dead code (#917 pattern, missed spot): the only caller chain runs
// after handleAgentDoneMsg already zeroed the field, so the '(N tool calls)'
// fallback never fired.
func (m Model) buildAgentTitleSummary(toolCount int) string {
	if toolCount > 0 {
		return fmt.Sprintf("Agent task (%d tool calls)", toolCount)
	}
	return ""
}

// shortPath returns the last 1-2 path segments of a file path for brevity.
func shortPath(path string) string {
	segs := strings.Split(strings.ReplaceAll(path, "\\", "/"), "/")
	if len(segs) <= 2 {
		return strings.Join(segs, "/")
	}
	return strings.Join(segs[len(segs)-2:], "/")
}
