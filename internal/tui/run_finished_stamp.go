package tui

import "time"

// appendRunFinishedStamp closes the turn with the wall-clock time the agent
// loop ended. Someone coming back hours (or days) later can see when the work
// stopped without digging through tool timestamps; the resumed-session recap
// covers the restart case, this covers the live one.
func (m *Model) appendRunFinishedStamp(canceled, failed bool) {
	at := m.runEndedAt
	if at.IsZero() {
		at = time.Now()
	}
	stamp := at.Format("2006-01-02 15:04:05")
	switch {
	case canceled:
		m.chatWriteSystem(nextSystemID(), m.t("turn.canceled_at", stamp))
	case failed:
		m.chatWriteSystem(nextSystemID(), m.t("turn.failed_at", stamp))
	default:
		m.chatWriteSystem(nextSystemID(), m.t("turn.finished_at", stamp))
	}
}
