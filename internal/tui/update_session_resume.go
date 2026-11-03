package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/debug"
)

func (m *Model) handleSessionResumeLoaded(msg sessionResumeLoadedMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		return *m, func() tea.Msg {
			return streamMsg(m.t("session.resume_failed", msg.requestedID, msg.err))
		}
	}

	if m.pendingResumeID != "" && msg.requestedID != m.pendingResumeID {
		debug.Log("tui", "dropping stale resume result for %q (latest: %q)", msg.requestedID, m.pendingResumeID)
		return *m, nil
	}
	m.applyResumedSession(msg.session)
	title := msg.session.Title
	if title == "" {
		title = m.t("session.untitled")
	}
	return *m, tea.Batch(
		func() tea.Msg {
			return streamMsg(m.t("session.resume", msg.session.ID, title, len(msg.session.Messages)))
		},
		publishCurrentSessionCmd(true),
	)
}
