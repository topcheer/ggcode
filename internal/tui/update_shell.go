package tui

import (
	tea "charm.land/bubbletea/v2"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
	"strings"
)

// handleShellCommandStreamMsg handles the corresponding message case.
func (m Model) handleShellCommandStreamMsg(msg shellCommandStreamMsg, spinnerCmd tea.Cmd) (Model, tea.Cmd) {
	if msg.RunID != m.activeShellRunID || m.runCanceled || !m.loading {
		return m, nil
	}
	m.appendShellChunk(msg.Text)
	return m, combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))

}

// handleShellCommandDoneMsg handles the corresponding message case.
func (m Model) handleShellCommandDoneMsg(msg shellCommandDoneMsg) (Model, tea.Cmd) {
	if msg.RunID != m.activeShellRunID {
		return m, nil
	}
	hadShellOutput := m.shellBuffer != nil && m.shellBuffer.Len() > 0
	m.shellBuffer = nil
	m.shellOutputID = ""
	m.loading = false
	m.spinner.Stop()
	m.cancelFunc = nil
	wasCanceled := m.runCanceled
	wasFailed := m.runFailed
	m.runCanceled = false
	m.runFailed = false
	m.statusActivity = ""
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	// Auto-exit shell mode so user returns to the prompt
	m.setShellMode(false)
	if msg.Status == toolpkg.CommandJobFailed || msg.Status == toolpkg.CommandJobTimedOut {
		m.runFailed = true
		if m.pendingSubmissionCount() > 0 {
			m.restorePendingInput()
		}
		if text := strings.TrimSpace(msg.ErrText); text != "" {
			m.chatWriteSystem(nextSystemID(), text)
		}
	}
	if !wasCanceled && (hadShellOutput || strings.TrimSpace(msg.ErrText) != "") {
	}
	if msg.Status == toolpkg.CommandJobCompleted && m.pendingSubmissionCount() > 0 && !wasCanceled && !wasFailed {
		return m, m.submitShellCommand(m.consumePendingSubmission(), false)
	}
	m.chatListScrollToBottom()
	return m, nil

}
