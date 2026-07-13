package tui

import (
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// persistFullSessionMessages updates in-memory session state after an agent run.
//
// With per-message persistence (SetPersistHandler), each message is already
// written to JSONL via AppendMessageToDisk at Add() time. This function only
// needs to update ses.Messages for in-memory rendering and track the count.
//
// The batch AppendMessagesBatchToDisk path is no longer needed.
func (m *Model) persistFullSessionMessages() {
	if m == nil || m.agent == nil || m.session == nil || m.sessionStore == nil {
		debug.Log("tui", "persistFullSessionMessages: skip (nil agent/session/store)")
		return
	}
	m.sessionMutex().Lock()

	ses := m.session

	// Get all messages the agent added during this run.
	runAdded := m.agent.AddedSinceRunStart()

	// runAdded[0] is always the user submission, added by
	// RunStreamWithContent before the loop. It was already persisted by
	// appendUserMessage() before the agent run started.
	var newMsgs []provider.Message
	if len(runAdded) > 0 && runAdded[0].Role == "user" {
		newMsgs = runAdded[1:]
	} else {
		newMsgs = runAdded
	}

	// Append new messages to ses.Messages (for in-memory rendering).
	ses.Messages = append(ses.Messages, newMsgs...)
	m.persistedMsgCount = len(ses.Messages)
	m.sessionMutex().Unlock()
}
