package tui

import (
	"github.com/topcheer/ggcode/internal/agentruntime"
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
// APPROACH: The JSONL file is append-only. We never rewrite it. Each record
// is an immutable event.
//
// After an agent run, we need to find the assistant/tool messages that were
// added during this run and persist them. The user message was already
// persisted by appendUserMessage().
//
// We use a fingerprint-matching approach: walk backwards from the end of both
// ses.Messages and agent.Messages() to find the last overlapping message.
// Everything in agent.Messages() after that overlap is new and should be
// persisted. This works correctly even after compaction because the agent
// appends new messages (assistant, tool_result) after the compacted region.
func (m *Model) persistFullSessionMessages() {
	if m == nil || m.agent == nil || m.session == nil || m.sessionStore == nil {
		return
	}
	m.sessionMutex().Lock()

	ses := m.session
	store := m.sessionStore

	agentMsgs := m.agent.Messages()
	sesMsgs := ses.Messages

	// Find new messages by fingerprint matching.
	newMsgs := findNewMessages(sesMsgs, agentMsgs)

	// Append new messages to ses.Messages (for in-memory rendering).
	ses.Messages = append(ses.Messages, newMsgs...)

	// Update persistedMsgCount.
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

// findNewMessages returns messages from agentMsgs that are NOT in sesMsgs.
//
// Walk backwards from the end of both lists to find the overlap point.
// Messages after the overlap in agentMsgs are new.
//
// Example (post-compaction):
//
//	sesMsgs:     [user1, asst1, tool1, asst2, user2]
//	agentMsgs:   [summary, user1', user2, asst3, tool3]
//	                            ^ overlap starts here (user2)
//	newMsgs:     [asst3, tool3]
//
// The compacted summary and the replayed old messages (user1') are NOT
// in sesMsgs, but they're synthetic/history — not new conversation events.
// We skip them by only returning messages AFTER the last overlap point.
func findNewMessages(sesMsgs, agentMsgs []provider.Message) []provider.Message {
	if len(agentMsgs) == 0 {
		return nil
	}

	sesLen := len(sesMsgs)
	agentLen := len(agentMsgs)

	// Walk backwards from the end of both lists to find overlap count.
	overlap := 0
	maxCompare := sesLen
	if agentLen < maxCompare {
		maxCompare = agentLen
	}
	for i := 0; i < maxCompare; i++ {
		if !messagesEqual(sesMsgs[sesLen-1-i], agentMsgs[agentLen-1-i]) {
			break
		}
		overlap++
	}

	// Everything in agentMsgs after the overlap is new.
	newStart := agentLen - overlap
	if newStart <= 0 {
		return nil
	}
	return agentMsgs[newStart:]
}

// messagesEqual checks if two messages have the same content fingerprint.
func messagesEqual(a, b provider.Message) bool {
	if a.Role != b.Role {
		return false
	}
	if len(a.Content) != len(b.Content) {
		return false
	}
	for i := range a.Content {
		if !contentBlocksEqual(a.Content[i], b.Content[i]) {
			return false
		}
	}
	return true
}

func contentBlocksEqual(a, b provider.ContentBlock) bool {
	if a.Type != b.Type {
		return false
	}
	switch a.Type {
	case "text":
		return a.Text == b.Text
	case "tool_use":
		return a.ToolID == b.ToolID && a.ToolName == b.ToolName
	case "tool_result":
		return a.ToolID == b.ToolID
	default:
		return a.Text == b.Text
	}
}
