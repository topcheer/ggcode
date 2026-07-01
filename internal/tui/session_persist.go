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
// were added via contextManager.Add() during the current run. We get that
// list, filter out user text messages (already persisted by
// appendUserMessage), and append the rest (assistant responses, tool calls,
// tool results) to ses.Messages and the JSONL file.
//
// This is compaction-safe: ApplyCompactResult replaces m.messages directly
// (bypassing Add), so it doesn't pollute the run-added list.
func (m *Model) persistFullSessionMessages() {
	if m == nil || m.agent == nil || m.session == nil || m.sessionStore == nil {
		return
	}
	m.sessionMutex().Lock()

	ses := m.session
	store := m.sessionStore

	// Get all messages the agent added during this run.
	runAdded := m.agent.AddedSinceRunStart()

	// Filter out user text messages — those were already persisted by
	// appendUserMessage() before the agent run started. We keep:
	//   - assistant messages (Role: "assistant")
	//   - tool results (Role: "user" with tool_result content blocks)
	// We skip:
	//   - user text messages (Role: "user" with only text content blocks)
	//     — already persisted by appendUserMessage
	//   - system messages — internal, not conversation events
	var newMsgs []provider.Message
	for _, msg := range runAdded {
		if shouldPersistMessage(msg) {
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

// shouldPersistMessage decides whether a message added by the agent should
// be persisted to the JSONL file.
//
// We persist:
//   - assistant messages (the LLM's responses with text and/or tool_use blocks)
//   - user messages containing tool_result blocks (tool execution results)
//
// We skip:
//   - user messages with only text blocks (already persisted by appendUserMessage)
//   - system messages (internal, not conversation events)
//   - user messages with image-only content (already handled by appendUserMessage)
func shouldPersistMessage(msg provider.Message) bool {
	switch msg.Role {
	case "assistant":
		return true
	case "user":
		// Only persist user messages that contain tool_result blocks.
		// Pure text user messages were already persisted by appendUserMessage.
		for _, block := range msg.Content {
			if block.Type == "tool_result" {
				return true
			}
		}
		return false
	default:
		return false
	}
}
