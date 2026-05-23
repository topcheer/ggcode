package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"fmt"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/harness"
	"github.com/topcheer/ggcode/internal/provider"
	"strings"
)

// handleHarnessPanelRefreshResultMsg handles the corresponding message case.
func (m Model) handleHarnessPanelRefreshResultMsg(msg harnessPanelRefreshResultMsg) (Model, tea.Cmd) {
	m.applyHarnessPanelResult(msg)
	return m, nil

}

// handleAutoRunCheckResultMsg handles the corresponding message case.
func (m Model) handleAutoRunCheckResultMsg(msg autoRunCheckResultMsg) (Model, tea.Cmd) {
	m.loading = false
	m.spinner.Stop()
	m.statusActivity = ""
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	if msg.Err != nil {
		debug.Log("auto-run", "routing check failed; continuing normally: %v", msg.Err)
		return m, m.continueDisplayedNormalTextRun(msg.Text)
	}
	if msg.Result != nil {
		switch msg.Result.Decision {
		case harness.RouteSuggest:
			m.pendingAutoRun = msg.Result
			m.pendingAutoRunText = msg.Text
			m.chatWriteSystem(nextChatID(), msg.Result.Message)
			m.chatListScrollToBottom()
			return m, nil
		case harness.RouteHarness:
			return m, m.handleAutoRun(msg.Text, msg.Result)
		}
	}
	// No harness routing — continue to normal agent.
	return m, m.continueDisplayedNormalTextRun(msg.Text)

}

// handleHarnessRunResultMsg handles the corresponding message case.
func (m Model) handleHarnessRunResultMsg(msg harnessRunResultMsg) (Model, tea.Cmd) {
	if path := strings.TrimSpace(m.harnessRunLogPath); path != "" {
		chunk, nextOffset := readHarnessRunLogChunk(path, m.harnessRunLogOffset)
		m.harnessRunLogOffset = nextOffset
		m.appendHarnessLogChunk(chunk)
	} else if msg.Summary != nil && msg.Summary.Task != nil {
		path := strings.TrimSpace(msg.Summary.Task.LogPath)
		chunk, nextOffset := readHarnessRunLogChunk(path, m.harnessRunLogOffset)
		m.harnessRunLogPath = path
		m.harnessRunLogOffset = nextOffset
		m.appendHarnessLogChunk(chunk)
	}
	m.flushHarnessLogRemainder()
	m.loading = false
	m.spinner.Stop()
	m.chatFinishAllRunningTools()
	m.cancelFunc = nil
	wasCanceled := m.runCanceled
	wasFailed := m.runFailed
	m.runCanceled = false
	m.runFailed = false
	m.statusActivity = ""
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	m.harnessRunProject = nil
	m.harnessRunGoal = ""
	m.harnessRunTaskID = ""
	m.harnessRunLastDetail = ""
	m.harnessRunRemainder = ""
	m.harnessRunLiveTail = ""
	streamedHarnessOutput := m.harnessRunLogOffset > 0 || strings.TrimSpace(m.harnessRunLastDetail) != ""
	m.harnessRunLogPath = ""
	m.harnessRunLogOffset = 0
	if errors.Is(msg.Err, context.Canceled) {
		return m, nil
	}
	if msg.Err != nil {
		m.runFailed = true
		if m.pendingSubmissionCount() > 0 {
			m.restorePendingInput()
		}
		m.chatWriteSystem(nextSystemID(), msg.Err.Error())
		m.chatListScrollToBottom()
		return m, nil
	}
	rendered := harness.FormatRunSummary(msg.Summary)
	if streamedHarnessOutput {
		rendered = trimHarnessRunOutputSection(rendered)
	}
	// Append CTA (next action) - generate from summary if not provided
	ctaAction := msg.CTA
	ctaMsg := msg.CTAMessage
	if ctaMsg == "" && msg.Summary != nil {
		ctaAction, ctaMsg = harness.GenerateCTA(msg.Summary, msg.Err)
	}
	if ctaMsg != "" {
		rendered += fmt.Sprintf("\nNext: %s", ctaMsg)
	}
	// Set pending review for one-key approve/reject if CTA is review
	if ctaAction == harness.CTAReview && msg.Summary != nil && msg.Summary.Task != nil {
		m.pendingHarnessReview = msg.Summary.Task
		rendered += "\nPress Enter to approve, Esc to skip."
	}
	m.renderStreamBuffer(true)
	m.chatWriteSystem(nextSystemID(), rendered)
	m.chatListScrollToBottom()
	// Broadcast harness result to WebUI subscribers if available
	if m.webuiBridge != nil {
		m.webuiBridge.BroadcastEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: rendered})
	}
	if !wasCanceled && !wasFailed && m.pendingSubmissionCount() > 0 {
		return m, m.submitPendingSubmissionCmd()
	}
	return m, nil

}

// handleHarnessReviewResultMsg handles the corresponding message case.
func (m Model) handleHarnessReviewResultMsg(msg harnessReviewResultMsg) (Model, tea.Cmd) {
	m.loading = false
	m.spinner.Stop()
	if msg.Err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Review failed for task %s: %v", msg.TaskID, msg.Err))
	} else {
		status := "approved"
		if msg.Task != nil {
			status = string(msg.Task.ReviewStatus)
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Task %s review: %s", msg.TaskID, status))
		// If approved, set pending promote for one-key CTA
		if msg.Task != nil && msg.Task.ReviewStatus == harness.ReviewApproved {
			m.pendingHarnessPromote = msg.Task
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Task %s approved. Press Enter to promote, Esc to skip.", msg.TaskID))
		}
	}
	m.chatListScrollToBottom()
	if m.webuiBridge != nil {
		m.webuiBridge.BroadcastEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: fmt.Sprintf("Task %s review: done", msg.TaskID)})
	}
	return m, nil

}

// handleHarnessPromoteResultMsg handles the corresponding message case.
func (m Model) handleHarnessPromoteResultMsg(msg harnessPromoteResultMsg) (Model, tea.Cmd) {
	m.loading = false
	m.spinner.Stop()
	if msg.Err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Promote failed for task %s: %v", msg.TaskID, msg.Err))
	} else if msg.Task != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Task %s promoted successfully.", msg.TaskID))
	} else {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Task %s promote completed.", msg.TaskID))
	}
	m.chatListScrollToBottom()
	if m.webuiBridge != nil {
		m.webuiBridge.BroadcastEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: fmt.Sprintf("Task %s promoted", msg.TaskID)})
	}
	return m, nil

}

// handleKnightTaskResultMsg handles the corresponding message case.
func (m Model) handleKnightTaskResultMsg(msg knightTaskResultMsg) (Model, tea.Cmd) {
	m.loading = false
	m.spinner.Stop()
	m.cancelFunc = nil
	m.statusActivity = ""
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	if msg.Err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Knight task failed: %v", msg.Err))
		m.chatListScrollToBottom()
		return m, nil
	}
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("🌙 Knight task completed: %s", msg.Goal))
	m.chatWriteSystem(nextSystemID(), strings.TrimSpace(msg.Result.Output))
	m.chatListScrollToBottom()
	return m, nil

}

// handleKnightProjectProposalResultMsg handles the corresponding message case.
func (m Model) handleKnightProjectProposalResultMsg(msg knightProjectProposalResultMsg) (Model, tea.Cmd) {
	m.loading = false
	m.spinner.Stop()
	m.cancelFunc = nil
	m.statusActivity = ""
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	if msg.Err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Knight proposal failed: %v", msg.Err))
		m.chatListScrollToBottom()
		return m, nil
	}
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("📝 Knight proposal created: %s", msg.Proposal.Title))
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("ID: %s\nPath: %s\nReview with /knight proposals %s", msg.Proposal.ID, msg.Proposal.Path, msg.Proposal.ID))
	m.chatListScrollToBottom()
	return m, nil

}

// handleKnightTaskEventMsg handles the corresponding message case.
func (m Model) handleKnightTaskEventMsg(msg knightTaskEventMsg) (Model, tea.Cmd) {
	if msg.Report == "" {
		// Task started
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("🌙 Knight: starting %s", msg.TaskName))
	} else if msg.TaskName == "" {
		// Detailed report from emitReport (same as IM)
		m.chatWriteSystem(nextSystemID(), msg.Report)
	} else {
		// Task completed with summary
		suffix := ""
		if msg.Duration > 0 {
			suffix = fmt.Sprintf(" (%.0fs)", msg.Duration.Seconds())
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("🌙 Knight %s completed%s\n%s", msg.TaskName, suffix, msg.Report))
	}
	m.chatListScrollToBottom()
	return m, nil

}

// handleHarnessContextSuggestionsMsg handles the corresponding message case.
func (m Model) handleHarnessContextSuggestionsMsg(msg harnessContextSuggestionsMsg) (Model, tea.Cmd) {
	state := m.harnessContextPrompt
	if state == nil || state.mode != harnessContextPromptInit {
		return m, nil
	}
	state.step = harnessContextPromptStepSelect
	state.suggestions = harness.NormalizeContexts(msg.Contexts)
	state.selected = map[int]bool{}
	state.cursor = 0
	state.input.Placeholder = "Optional custom contexts: payments, checkout=apps/checkout"
	state.input.SetValue("")
	state.inputFocus = len(state.suggestions) == 0
	if state.inputFocus {
		state.input.Focus()
	} else {
		state.input.Blur()
	}
	if msg.Err != nil {
		state.message = msg.Err.Error()
	} else if len(state.suggestions) == 0 {
		state.message = "No suggestions found. Add custom contexts below."
	} else {
		state.message = ""
	}
	return m, nil

}

// handleHarnessInitResultMsg handles the corresponding message case.
func (m Model) handleHarnessInitResultMsg(msg harnessInitResultMsg) (Model, tea.Cmd) {
	state := m.harnessContextPrompt
	if msg.Err != nil {
		if state != nil {
			state.message = msg.Err.Error()
			if state.existingProject {
				state.step = harnessContextPromptStepUpgrade
			} else {
				state.step = harnessContextPromptStepSelect
			}
		}
		return m, nil
	}
	m.closeHarnessContextPrompt("")
	m.refreshHarnessPanel()
	if msg.Result != nil {
		commandText := "/harness init"
		if state != nil && strings.TrimSpace(state.commandText) != "" {
			commandText = strings.TrimSpace(state.commandText)
		}
		m.chatWriteUser(nextChatID(), commandText)
		m.appendUserMessage(commandText)
		m.chatWriteSystem(nextSystemID(), formatHarnessInitResult(msg.Result))
		m.chatListScrollToBottom()
		if panel := m.harnessPanel; panel != nil {
			panel.message = fmt.Sprintf("Initialized harness in %s", msg.Result.Project.RootDir)
		}
	}
	return m, nil

}

// handleHarnessRunProgressMsg handles the corresponding message case.
func (m Model) handleHarnessRunProgressMsg(msg harnessRunProgressMsg) (Model, tea.Cmd) {
	if !m.loading || m.harnessRunProject == nil {
		return m, nil
	}
	if msg.TaskID != "" {
		m.harnessRunTaskID = msg.TaskID
	}
	if msg.LogPath != "" {
		m.harnessRunLogPath = msg.LogPath
	}
	if msg.LogChunk != "" {
		m.appendHarnessLogChunk(msg.LogChunk)
	}
	if msg.LogOffset > 0 {
		m.harnessRunLogOffset = msg.LogOffset
	}
	if detail := strings.TrimSpace(msg.Detail); detail != "" && detail != m.harnessRunLastDetail {
		m.harnessRunLastDetail = detail
		if !harnessLogChunkContainsDetail(m.currentLanguage(), m.harnessRunProject, msg.LogChunk, detail) {
			m.appendHarnessProgressDetail(detail)
		}
	}
	if strings.TrimSpace(msg.Activity) != "" {
		m.statusActivity = msg.Activity
	}
	return m, m.pollHarnessRunProgress()

}

// handleHarnessPanelAutoRefreshMsg handles the corresponding message case.
func (m Model) handleHarnessPanelAutoRefreshMsg(msg harnessPanelAutoRefreshMsg) (Model, tea.Cmd) {
	if !m.shouldAutoRefreshHarnessTask() {
		return m, nil
	}
	cmd := m.refreshHarnessPanelForced()
	if !m.shouldAutoRefreshHarnessTask() {
		// Task completed — stop polling, but still return the refresh cmd
		// so the data arrives.
		return m, cmd
	}
	if cmd != nil {
		return m, tea.Batch(cmd, m.pollHarnessPanelAutoRefresh())
	}
	return m, m.pollHarnessPanelAutoRefresh()

}
