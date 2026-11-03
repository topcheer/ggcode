package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/lsp"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

func (m Model) handleShellCommandStreamMsg(msg shellCommandStreamMsg, spinnerCmd tea.Cmd) (Model, tea.Cmd) {
	if msg.RunID != m.activeShellRunID || m.runCanceled || !m.shellRunning {
		return m, nil
	}
	m.appendShellChunk(msg.Text)
	return m, combineCmds(spinnerCmd, m.ensureLoadingSpinner(shellStatusActivity(m.currentLanguage())))
}

func (m Model) handleShellCommandDoneMsg(msg shellCommandDoneMsg) (Model, tea.Cmd) {
	if msg.RunID != m.activeShellRunID {
		return m, nil
	}

	if m.lspInstallInFlight {
		m.lspInstallInFlight = false
		lsp.InvalidateProbeCache()
	}
	hadShellOutput := m.shellBuffer != nil && m.shellBuffer.Len() > 0
	shellOutputID := m.shellOutputID
	m.shellBuffer = nil
	m.shellOutputID = ""
	m.shellRunning = false

	shellOwnedRun := m.shellOwnedLoading

	if shellOwnedRun {
		m.shellOwnedLoading = false
		m.setLoading(false)
		m.statusActivity = ""
		m.statusToolName = ""
		m.statusToolArg = ""
		m.statusToolCount = 0
		m.shellCancelFunc = nil
		m.spinner.Stop()
	}

	wasCanceled := m.runCanceled
	wasFailed := m.runFailed

	if shellOwnedRun {
		m.runCanceled = false
		m.runFailed = false
	}

	if hadShellOutput && shellOutputID != "" {
		if broker := m.tunnelEventBroker(); broker != nil {
			broker.PushTextDone(shellOutputID)
		}
	}

	m.setShellMode(false)
	if msg.Status == toolpkg.CommandJobFailed || msg.Status == toolpkg.CommandJobTimedOut {
		if text := strings.TrimSpace(msg.ErrText); text != "" {
			m.chatWriteSystem(nextSystemID(), text)
		}
	}
	if msg.Status == toolpkg.CommandJobCompleted && m.pendingSubmissionCount() > 0 && !wasCanceled && !wasFailed {

		if m.pendingHeadIsAgentOrigin() {
			return m, m.submitPendingSubmissionCmd()
		}
		return m, m.submitShellCommand(m.consumePendingSubmission(), false)
	}
	m.chatListFollowOutput()
	return m, nil
}
