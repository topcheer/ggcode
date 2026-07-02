package tui

import (
	"github.com/topcheer/ggcode/internal/debug"
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
// We persist all of them EXCEPT the leading user message that was already
// persisted by appendUserMessage().
//
// How we identify the already-persisted user message:
// appendUserMessage() adds the raw user text to ses.Messages and writes it
// to disk. Then startAgent() → RunStreamWithContent() adds the user message
// (possibly with prefix/image annotations) via contextManager.Add().
// Both appear at the start of runAdded. We skip the first user message
// (by position), then persist everything else.
//
// Compaction safety: ApplyCompactResult replaces m.messages directly
// (bypassing Add), so it does NOT pollute runAdded. Messages added before
// compaction but during the same run are still tracked.
func (m *Model) persistFullSessionMessages() {
	if m == nil || m.agent == nil || m.session == nil || m.sessionStore == nil {
		return
	}
	m.sessionMutex().Lock()

	ses := m.session
	store := m.sessionStore

	// Get all messages the agent added during this run.
	runAdded := m.agent.AddedSinceRunStart()

	// The first message in runAdded is always the user submission that
	// triggered this run. It was already persisted by appendUserMessage()
	// before the agent run started. Skip it.
	//
	// Note: appendUserMessage persists the raw user text. The agent's
	// contextManager.Add may store a modified version (with image path
	// hints, interrupt prefixes, etc.). We skip the agent's copy and rely
	// on the one already on disk.
	//
	// Everything else in runAdded needs to be persisted:
	//   - assistant messages (LLM responses with text and/or tool_use)
	//   - tool_result messages (role:user with tool_result content blocks)
	//   - interrupt injections (role:user with "New user guidance..." text)
	//   - autopilot synthetic messages (continue, ask_user, goal_check)
	//   - empty-response nudges ("The previous response was empty...")
	//   - deferred project memory system messages
	newMsgs := runAdded
	if len(newMsgs) > 0 && newMsgs[0].Role == "user" {
		newMsgs = newMsgs[1:]
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
