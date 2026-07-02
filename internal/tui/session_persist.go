package tui

import (
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

// persistFullSessionMessages appends only NEW messages (since the last
// persist) to the JSONL file using AppendMessageToDisk().
//
// ⚠️ CRITICAL: This function must NEVER call Save() or
// SaveAgentSessionSnapshot(). Those methods do a full file rewrite using
// agent.Messages() (which is the COMPACTED version). That would permanently
// destroy pre-compaction message records from the JSONL file.
//
// APPROACH: The JSONL file is append-only. The agent tracks which messages
// were added via contextManager.Add() during the current run (runAdded).
// We persist all of them EXCEPT those already in ses.Messages (which were
// persisted by appendUserMessage() before the run started).
//
// Dedup is necessary because some entry points call appendUserMessage()
// before startAgent(), writing the user message to disk immediately. Those
// same user messages also appear in runAdded (added by RunStreamWithContent).
// But other entry points (submitLanChatAgentText, custom commands) skip
// appendUserMessage() — their user messages only exist in runAdded and
// would be lost without persisting them here.
//
// Compaction safety: ApplyCompactResult replaces m.messages directly
// (bypassing Add), so it does NOT pollute runAdded.
func (m *Model) persistFullSessionMessages() {
	if m == nil || m.agent == nil || m.session == nil || m.sessionStore == nil {
		return
	}
	m.sessionMutex().Lock()

	ses := m.session
	store := m.sessionStore

	// Get all messages the agent added during this run.
	runAdded := m.agent.AddedSinceRunStart()

	// Dedup against ses.Messages: skip messages already present.
	// appendUserMessage() may have already persisted the leading user message.
	// But other paths (lanchat, custom commands) did not — their user message
	// must be persisted here.
	existing := ses.Messages
	var newMsgs []provider.Message
	for _, msg := range runAdded {
		if !containsMessage(existing, msg) {
			newMsgs = append(newMsgs, msg)
		}
	}

	// Append new messages to ses.Messages (for in-memory rendering).
	ses.Messages = append(ses.Messages, newMsgs...)
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
	}
}

// containsMessage checks whether msgs contains a message with the same
// role and content fingerprint as target.
func containsMessage(msgs []provider.Message, target provider.Message) bool {
	for _, msg := range msgs {
		if messageFingerprint(msg) == messageFingerprint(target) {
			return true
		}
	}
	return false
}

// messageFingerprint produces a lightweight identity hash for a message.
// Two messages are considered "the same record" if role + all content
// block types + text/tool IDs match.
func messageFingerprint(msg provider.Message) string {
	h := msg.Role
	for _, b := range msg.Content {
		h += "|" + b.Type
		switch b.Type {
		case "text":
			h += ":" + b.Text
		case "tool_use":
			h += ":" + b.ToolName + ":" + b.ToolID
		case "tool_result":
			h += ":" + b.ToolID
		case "image":
			h += ":img"
		}
	}
	return h
}
