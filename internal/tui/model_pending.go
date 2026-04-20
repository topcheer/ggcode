package tui

import (
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

func (m *Model) resetExitConfirm() {
	m.exitConfirmPending = false
}

func (m *Model) promptExitConfirm() {
	m.input.SetValue("")
	m.exitConfirmPending = true
	m.ensureOutputHasBlankLine()
	m.output.WriteString(m.styles.prompt.Render(m.t("exit.confirm")))
	m.output.WriteString("\n")
	m.syncConversationViewport()
	if m.viewport.AutoFollow() {
		m.viewport.GotoBottom()
	}
}

func (m *Model) queuePendingSubmission(text string) {
	count := m.enqueuePendingSubmission(text)
	debug.Log("tui", "queuePendingSubmission: count=%d text=%s", count, truncateStr(text, 100))
	if count == 0 {
		return
	}
	// Render the user's input in the conversation view so it looks like a
	// normal submission, rather than showing a "[queued N pending]" hint.
	m.ensureOutputHasBlankLine()
	m.output.WriteString(m.renderConversationUserEntry("❯ ", text))
	m.output.WriteString("\n")
	m.syncConversationViewport()
	if m.viewport.AutoFollow() {
		m.viewport.GotoBottom()
	}
}

func (m *Model) enqueuePendingSubmission(text string) int {
	m.pendingMutex().Lock()
	defer m.pendingMutex().Unlock()
	m.pendingSubmissions = append(m.pendingSubmissions, text)
	return len(m.pendingSubmissions)
}

func (m *Model) pendingSubmissionCount() int {
	m.pendingMutex().Lock()
	defer m.pendingMutex().Unlock()
	return len(m.pendingSubmissions)
}

func (m *Model) clearPendingSubmissions() {
	m.pendingMutex().Lock()
	defer m.pendingMutex().Unlock()
	m.pendingSubmissions = nil
}

func (m *Model) pendingSubmissionSnapshot() []string {
	m.pendingMutex().Lock()
	defer m.pendingMutex().Unlock()
	if len(m.pendingSubmissions) == 0 {
		return nil
	}
	out := make([]string, len(m.pendingSubmissions))
	copy(out, m.pendingSubmissions)
	return out
}

func (m *Model) cancelActiveRun() {
	if m.runCanceled {
		return
	}
	m.runCanceled = true
	if m.cancelFunc != nil {
		m.cancelFunc()
	}
	m.spinner.Stop()
	if m.harnessRunProject != nil {
		m.statusActivity = m.t("status.cancelling")
	} else {
		m.loading = false
		m.cancelFunc = nil
		m.statusActivity = ""
	}
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	m.resetActivityGroups()
	if m.pendingSubmissionCount() > 0 {
		m.restorePendingInput()
	}
	debug.Log("tui", "cancelling active loop")
	m.output.WriteString("\n" + m.t("interrupted"))
	m.syncConversationViewport()
	if m.viewport.AutoFollow() {
		m.viewport.GotoBottom()
	}
}

func (m *Model) consumePendingSubmission() string {
	m.pendingMutex().Lock()
	defer m.pendingMutex().Unlock()
	joined := strings.TrimSpace(strings.Join(m.pendingSubmissions, "\n\n"))
	m.pendingSubmissions = nil
	return joined
}

func (m *Model) restorePendingInput() {
	m.pendingMutex().Lock()
	pending := strings.TrimSpace(strings.Join(m.pendingSubmissions, "\n\n"))
	draft := strings.TrimSpace(m.input.Value())
	switch {
	case pending == "":
		m.pendingMutex().Unlock()
		return
	case draft == "":
		m.input.SetValue(pending)
	case draft == pending:
		m.input.SetValue(draft)
	default:
		m.input.SetValue(pending + "\n\n" + draft)
	}
	m.input.CursorEnd()
	m.pendingSubmissions = nil
	m.pendingMutex().Unlock()
}

func (m *Model) drainPendingInterrupt(runID int) string {
	text := m.consumePendingSubmission()
	if text == "" {
		return ""
	}
	debug.Log("tui", "drainPendingInterrupt: runID=%d text=%s", runID, truncateStr(text, 100))
	m.appendUserMessage(text)
	// Don't send agentInterruptMsg — the user already saw their input rendered
	// in the conversation when it was queued. No extra "[delivered]" hint needed.
	return text
}

func (m *Model) pendingMutex() *sync.Mutex {
	if m.pendingMu == nil {
		m.pendingMu = &sync.Mutex{}
	}
	return m.pendingMu
}

func (m *Model) sessionMutex() *sync.Mutex {
	if m.sessionMu == nil {
		m.sessionMu = &sync.Mutex{}
	}
	return m.sessionMu
}

func stripImagePlaceholder(value, placeholder string) string {
	trimmed := strings.TrimSpace(value)
	placeholder = strings.TrimSpace(placeholder)
	if trimmed == "" || placeholder == "" {
		return trimmed
	}
	if trimmed == placeholder {
		return ""
	}
	if strings.HasPrefix(trimmed, placeholder) {
		return strings.TrimSpace(strings.TrimPrefix(trimmed, placeholder))
	}
	return trimmed
}

func (m *Model) stripPendingImagePlaceholder(value string) string {
	if m.pendingImage == nil {
		return strings.TrimSpace(value)
	}
	return stripImagePlaceholder(value, m.pendingImage.placeholder)
}

func (m *Model) setComposerImagePlaceholder(msg imageAttachedMsg) {
	draft := m.input.Value()
	if m.pendingImage != nil {
		draft = stripImagePlaceholder(draft, m.pendingImage.placeholder)
	}
	draft = strings.TrimSpace(draft)
	if draft == "" {
		m.input.SetValue(msg.placeholder + " ")
	} else {
		m.input.SetValue(msg.placeholder + " " + draft)
	}
	m.input.CursorEnd()
}
