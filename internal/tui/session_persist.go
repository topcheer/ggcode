package tui

import (
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

func (m *Model) persistFullSessionMessages() {
	if m == nil || m.agent == nil || m.session == nil || m.sessionStore == nil {
		debug.Log("tui", "persistFullSessionMessages: skip (nil agent/session/store)")
		return
	}
	m.sessionMutex().Lock()

	ses := m.session

	runAdded := m.agent.AddedSinceRunStart()

	var newMsgs []provider.Message
	if len(runAdded) > 0 && runAdded[0].Role == "user" {
		newMsgs = runAdded[1:]
	} else {
		newMsgs = runAdded
	}

	ses.Messages = append(ses.Messages, newMsgs...)
	m.persistedMsgCount = len(ses.Messages)
	m.sessionMutex().Unlock()
}
