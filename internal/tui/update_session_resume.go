package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/debug"
)

// handleSessionResumeLoaded applies an async session-resume Load result
// (#2357 slice 6, the tail slice: extracted from Model.Update - body moved
// verbatim, zero behavior change). The #1755/#1887 stale-result guard and
// the resume notification batch are preserved as-is.
func (m *Model) handleSessionResumeLoaded(msg sessionResumeLoadedMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		return *m, func() tea.Msg {
			return streamMsg(m.t("session.resume_failed", msg.requestedID, msg.err))
		}
	}
	// #1755/#1887: a slow OLDER Load completing after a newer request must
	// not clobber the latest resume - drop the stale result. The key is
	// deliberately NOT cleared on match: clearing it re-opened the
	// newer-first race (B completes, clears the key, then A arrives to
	// an empty key and sails through the != "" guard). Inequality
	// against the last request is immune to both completion orders.
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
