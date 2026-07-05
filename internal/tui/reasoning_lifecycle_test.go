package tui

import (
	"testing"
)

// TestReasoningStartsOnFirstChunk verifies that reasoningActive is set true
// when the first reasoning chunk arrives, enabling the thinking block to expand.
func TestReasoningStartsOnFirstChunk(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 7

	next, _ := m.handleAgentReasoningMsg(agentReasoningMsg{RunID: 7, Text: "let me think..."}, nil)
	m = next

	if !m.reasoningActive {
		t.Fatal("reasoningActive should be true after first reasoning chunk (reasoning block must expand)")
	}
	if !m.streamPrefixWritten {
		t.Fatal("streamPrefixWritten should be true after first reasoning chunk")
	}
}

// TestReasoningCollapsesOnFirstTextChunk verifies that when text arrives
// after reasoning, the reasoning block collapses (reasoningActive=false).
func TestReasoningCollapsesOnFirstTextChunk(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 7

	// Turn 1: reasoning
	next, _ := m.handleAgentReasoningMsg(agentReasoningMsg{RunID: 7, Text: "thinking..."}, nil)
	m = next
	if !m.reasoningActive {
		t.Fatal("reasoningActive should be true after reasoning chunk")
	}

	// Turn 1: first text chunk arrives → must collapse reasoning
	next, _ = m.handleAgentStreamMsg(agentStreamMsg{RunID: 7, Text: "hello"}, nil)
	m = next

	if m.reasoningActive {
		t.Fatal("reasoningActive should be false after first text chunk (reasoning must collapse)")
	}
}

// TestReasoningCollapsesOnFirstToolEvent verifies that when a tool event
// arrives after reasoning (no text in between), the reasoning block collapses.
func TestReasoningCollapsesOnFirstToolEvent(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 7

	// Reasoning arrives
	next, _ := m.handleAgentReasoningMsg(agentReasoningMsg{RunID: 7, Text: "I need to read a file"}, nil)
	m = next
	if !m.reasoningActive {
		t.Fatal("reasoningActive should be true after reasoning chunk")
	}

	// Tool start arrives → must collapse reasoning
	ts := toolStatusMsg{ToolName: "read_file", Detail: "main.go", Running: true}
	next, _ = m.handleToolStatusMsg(ts, nil)
	m = next

	if m.reasoningActive {
		t.Fatal("reasoningActive should be false after first tool event (reasoning must collapse)")
	}
}

// TestTextDoesNotFragmentAcrossToolResults verifies that text before and after
// a tool call stays on the SAME assistant item within one LLM turn.
func TestTextDoesNotFragmentAcrossToolResults(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 7

	// Text chunk 1
	next, _ := m.handleAgentStreamMsg(agentStreamMsg{RunID: 7, Text: "before tool"}, nil)
	m = next
	idBefore := m.currentAssistantID()

	// Tool completes
	ts := toolStatusMsg{ToolName: "read_file", Detail: "test.go", Running: false}
	next, _ = m.handleToolStatusMsg(ts, nil)
	m = next

	// Text chunk 2 after tool result — must use the SAME assistant item
	next, _ = m.handleAgentStreamMsg(agentStreamMsg{RunID: 7, Text: "after tool"}, nil)
	m = next
	idAfter := m.currentAssistantID()

	if idBefore != idAfter {
		t.Fatalf("text fragmented: idBefore=%s idAfter=%s (should be same item within one turn)", idBefore, idAfter)
	}
}

// TestToolResultDoesNotResetStreamPrefix verifies that streamPrefixWritten
// stays true after a tool result, preventing new assistant item creation.
func TestToolResultDoesNotResetStreamPrefix(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 7

	// Start with reasoning to set streamPrefixWritten
	next, _ := m.handleAgentReasoningMsg(agentReasoningMsg{RunID: 7, Text: "thinking"}, nil)
	m = next

	// Tool completes
	ts := toolStatusMsg{ToolName: "grep", Detail: "*.go", Running: false}
	next, _ = m.handleToolStatusMsg(ts, nil)
	m = next

	if !m.streamPrefixWritten {
		t.Fatal("streamPrefixWritten must stay true after tool result (prevents text fragmentation)")
	}
}

// TestChatFinishReasoningIdempotent verifies that calling chatFinishReasoning
// when reasoning is not active is a no-op.
func TestChatFinishReasoningIdempotent(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 7
	m.reasoningActive = false

	// Should be a no-op — no panic, no state change
	m.chatFinishReasoning()

	if m.reasoningActive {
		t.Fatal("chatFinishReasoning should be no-op when reasoning not active")
	}
}

// TestEachLLMTurnGetsSeparateAssistantItem verifies that after agentTurnDoneMsg,
// the next reasoning/text creates a NEW assistant item (one bubble per LLM turn).
func TestEachLLMTurnGetsSeparateAssistantItem(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 7

	// Turn 1: reasoning + text
	next, _ := m.handleAgentReasoningMsg(agentReasoningMsg{RunID: 7, Text: "turn 1"}, nil)
	m = next
	id1 := m.currentAssistantID()

	// End of turn 1
	updatedModel, _ := m.Update(agentTurnDoneMsg{})
	m = updatedModel.(Model)

	if m.streamPrefixWritten {
		t.Fatal("streamPrefixWritten should be false after agentTurnDoneMsg")
	}
	if m.streamBuffer != nil && m.streamBuffer.Len() != 0 {
		t.Fatalf("streamBuffer should be empty after agentTurnDoneMsg, got %d bytes", m.streamBuffer.Len())
	}

	// Turn 2: new reasoning → must create new item
	next, _ = m.handleAgentReasoningMsg(agentReasoningMsg{RunID: 7, Text: "turn 2"}, nil)
	m = next
	id2 := m.currentAssistantID()

	if id1 == id2 {
		t.Fatalf("turn 2 should create new assistant item (id1=%s id2=%s)", id1, id2)
	}
}

// TestAgentDoneFinalizesEverything verifies that agentDoneMsg resets the
// streaming state so the next user message starts fresh.
// Note: reasoningActive is reset by agentTurnDoneMsg which always fires
// before agentDoneMsg in normal flow. We verify loading=false here.
func TestAgentDoneFinalizesEverything(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 7

	// Simulate active streaming
	next, _ := m.handleAgentReasoningMsg(agentReasoningMsg{RunID: 7, Text: "thinking"}, nil)
	m = next
	m.handleAgentStreamMsg(agentStreamMsg{RunID: 7, Text: "result"}, nil)

	// agentTurnDoneMsg fires first (collapses reasoning, resets streamPrefix)
	updatedModel, _ := m.Update(agentTurnDoneMsg{})
	m = updatedModel.(Model)

	// Agent done
	updatedModel, _ = m.Update(agentDoneMsg{RunID: 7})
	m = updatedModel.(Model)

	if m.reasoningActive {
		t.Fatal("reasoningActive should be false after turn-done + agent-done")
	}
	if m.loading {
		t.Fatal("loading should be false after agentDoneMsg")
	}
}
