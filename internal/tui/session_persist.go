package tui

import (
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

// persistFullSessionMessages appends only NEW messages (since the last persist)
// to the JSONL file. It does NOT call Save() (full rewrite), which would
// destroy pre-compaction message history.
//
// The agent's context manager may compact messages (replacing earlier turns
// with a summary), but ses.Messages must retain ALL messages for correct
// rendering on session reload. Compaction only affects what gets sent to the
// LLM, not what gets persisted.
func (m *Model) persistFullSessionMessages() {
	if m == nil || m.agent == nil || m.session == nil || m.sessionStore == nil {
		return
	}
	m.sessionMutex().Lock()

	ses := m.session
	store := m.sessionStore

	// Get the agent's current messages (includes any compaction summary).
	agentMsgs := m.agent.Messages()
	agentCount := len(agentMsgs)

	// Determine which messages are new (not yet persisted to disk).
	// persistedMsgCount tracks how many messages from the beginning of
	// ses.Messages have been written to the JSONL file.
	newStart := m.persistedMsgCount
	if newStart < 0 {
		newStart = 0
	}

	// Sync ses.Messages with agent messages if the agent has more.
	// After compaction, agentMsgs may be SHORTER than ses.Messages —
	// in that case we don't truncate ses.Messages, we just don't
	// append anything new.
	if agentCount > len(ses.Messages) {
		// Agent has messages beyond what ses.Messages holds — append them.
		for i := len(ses.Messages); i < agentCount; i++ {
			ses.Messages = append(ses.Messages, agentMsgs[i])
		}
	}

	// Collect new messages to persist.
	var newMsgs []provider.Message
	if newStart < len(ses.Messages) {
		newMsgs = ses.Messages[newStart:]
	}

	m.persistedMsgCount = len(ses.Messages)
	m.sessionMutex().Unlock()

	if len(newMsgs) == 0 {
		return
	}

	// Append only the new messages to disk (incremental, no full rewrite).
	if jsonlStore, ok := store.(*session.JSONLStore); ok {
		for _, msg := range newMsgs {
			if err := jsonlStore.AppendMessageToDisk(ses, msg); err != nil {
				debug.Log("tui", "persistFullSessionMessages: AppendMessageToDisk failed: %v", err)
			}
		}
	} else {
		// Non-JSONLStore fallback: full Save (unchanged behavior).
		m.sessionMutex().Lock()
		_ = agentruntime.SaveAgentSessionSnapshot(store, ses, m.agent)
		m.sessionMutex().Unlock()
	}
}
