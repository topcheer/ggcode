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
// APPROACH: All entry points (keyboard, lanchat, custom commands, webui,
// tunnel, harness, autopilot) now call appendUserMessage() before starting
// the agent. appendUserMessage writes the user message to disk immediately
// and appends it to ses.Messages.
//
// RunStreamWithContent calls StartRunTracking() (clearing runAdded), then
// adds the user message via contextManager.Add() as runAdded[0]. Everything
// after runAdded[0] (assistant responses, tool results, interrupt injections,
// autopilot synthetic messages, etc.) needs to be persisted here.
//
// So we simply skip runAdded[0] (already on disk) and persist [1:].
//
// Compaction safety: ApplyCompactResult replaces m.messages directly
// (bypassing Add), so it does NOT pollute runAdded.
func (m *Model) persistFullSessionMessages() {
	if m == nil || m.agent == nil || m.session == nil || m.sessionStore == nil {
		debug.Log("tui", "persistFullSessionMessages: skip (nil agent/session/store)")
		return
	}
	m.sessionMutex().Lock()

	ses := m.session
	store := m.sessionStore

	// Get all messages the agent added during this run.
	runAdded := m.agent.AddedSinceRunStart()

	// runAdded[0] is always the user submission, added by
	// RunStreamWithContent before the loop. It was already persisted by
	// appendUserMessage() before the agent run started.
	//
	// Everything else (runAdded[1:]) needs to be persisted:
	//   - assistant messages (LLM responses with text and/or tool_use)
	//   - tool_result messages (role:user with tool_result content blocks)
	//   - interrupt injections (role:user with "New user guidance..." text)
	//   - autopilot synthetic messages (continue, ask_user, goal_check)
	//   - empty-response nudges ("The previous response was empty...")
	//   - deferred project memory system messages
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
		// Fallback for non-JSONL stores: full save.
		m.sessionMutex().Lock()
		_ = store.Save(ses)
		m.sessionMutex().Unlock()
	}
}
