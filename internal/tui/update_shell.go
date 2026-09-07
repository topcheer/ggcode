package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/lsp"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

// handleShellCommandStreamMsg handles the corresponding message case.
func (m Model) handleShellCommandStreamMsg(msg shellCommandStreamMsg, spinnerCmd tea.Cmd) (Model, tea.Cmd) {
	if msg.RunID != m.activeShellRunID || m.runCanceled || !m.shellRunning {
		return m, nil
	}
	m.appendShellChunk(msg.Text)
	return m, combineCmds(spinnerCmd, m.ensureLoadingSpinner(shellStatusActivity(m.currentLanguage())))
}

// handleShellCommandDoneMsg handles the corresponding message case.
func (m Model) handleShellCommandDoneMsg(msg shellCommandDoneMsg) (Model, tea.Cmd) {
	if msg.RunID != m.activeShellRunID {
		return m, nil
	}
	// #1653: an LSP install command just finished (success, failure, or
	// timeout). Drop the probe cache NOW - any negative entry primed while
	// the install was still running (panel reopen, post-edit diagnostics)
	// must not hide the freshly installed server for another 10 minutes.
	// Same semantics as the desktop side (wailskit lsp.go: success or
	// failure, one extra probe is cheap).
	if m.lspInstallInFlight {
		m.lspInstallInFlight = false
		lsp.InvalidateProbeCache()
	}
	hadShellOutput := m.shellBuffer != nil && m.shellBuffer.Len() > 0
	shellOutputID := m.shellOutputID
	m.shellBuffer = nil
	m.shellOutputID = ""
	m.shellRunning = false

	// Only clear loading if shell "owns" it (agent wasn't running when shell started).
	if m.shellOwnedLoading {
		m.shellOwnedLoading = false
		m.setLoading(false)
		m.statusActivity = ""
		m.statusToolName = ""
		m.statusToolArg = ""
		m.statusToolCount = 0
		m.shellCancelFunc = nil // #910
		m.spinner.Stop()
	}

	wasCanceled := m.runCanceled
	wasFailed := m.runFailed
	// #915/#1391-A: only the run that OWNS these flags may clear them.
	// The condition was inverted (!shellOwnedLoading): the shell clearing
	// ran exactly when the SHELL did NOT own the run - wiping the AGENT's
	// cancel/fail state, sending a cancelled agent down the normal-
	// completion path (duplicate session persist, metrics digest, swallowed
	// pending restore). commit 652104df's message states the intended
	// semantics: clear ONLY when the shell owns the run.
	if m.shellOwnedLoading {
		m.runCanceled = false
		m.runFailed = false
	}

	if hadShellOutput && shellOutputID != "" {
		if broker := m.tunnelEventBroker(); broker != nil {
			broker.PushTextDone(shellOutputID)
		}
	}
	// Auto-exit shell mode so user returns to the prompt
	m.setShellMode(false)
	if msg.Status == toolpkg.CommandJobFailed || msg.Status == toolpkg.CommandJobTimedOut {
		if text := strings.TrimSpace(msg.ErrText); text != "" {
			m.chatWriteSystem(nextSystemID(), text)
		}
	}
	if msg.Status == toolpkg.CommandJobCompleted && m.pendingSubmissionCount() > 0 && !wasCanceled && !wasFailed {
		return m, m.submitShellCommand(m.consumePendingSubmission(), false)
	}
	m.chatListFollowOutput()
	return m, nil
}
