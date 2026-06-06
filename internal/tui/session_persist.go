package tui

import "github.com/topcheer/ggcode/internal/agentruntime"

func (m *Model) persistFullSessionMessages() {
	if m == nil || m.agent == nil || m.session == nil || m.sessionStore == nil {
		return
	}
	m.sessionMutex().Lock()
	defer m.sessionMutex().Unlock()
	_ = agentruntime.SaveAgentSessionSnapshot(m.sessionStore, m.session, m.agent)
}
