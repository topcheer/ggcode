package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

func (m *Model) maybeBlindSpotRetry(err error) tea.Cmd {
	if !provider.IsBlindSpotError(err) {
		return nil
	}

	wasEnabled, logPath := debug.EnsureFileLogging()
	notice := m.blindSpotLogNotice(wasEnabled, logPath)
	if m.lastUserSubmission != "" {
		notice += " " + m.t("error.blindspot_retry_hint")
	}
	m.chatWriteSystem(nextSystemID(), notice)
	m.chatListScrollToBottom()
	return nil
}

func (m *Model) blindSpotLogNotice(wasEnabled bool, logPath string) string {
	if logPath == "" {
		logPath = m.t("error.blindspot_log_failed")
	}
	if wasEnabled {
		return fmt.Sprintf(m.t("error.blindspot_log_only"), logPath)
	}
	return fmt.Sprintf(m.t("error.blindspot_log_enabled"), logPath)
}

func (m *Model) resetBlindSpotRetry() {
	m.blindSpotRetries = 0
}
