package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/chat"
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

// TestTextContinuesAcrossTurns verifies that text from consecutive LLM turns
// accumulates in the SAME assistant item (no message fragmentation).
// Turn 1 writes "hello", agentTurnDoneMsg fires, turn 2 writes "world".
// The assistant item should contain "helloworld" — text is continuous.
func TestTextContinuesAcrossTurns(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 7

	// Turn 1: text
	next, _ := m.handleAgentStreamMsg(agentStreamMsg{RunID: 7, Text: "hello"}, nil)
	m = next
	id1 := m.currentAssistantID()

	// Verify turn 1 text
	if item := m.chatList.FindByID(id1); item != nil {
		if a, ok := item.(*chat.AssistantItem); ok {
			if a.Text() != "hello" {
				t.Fatalf("turn 1 text = %q, want %q", a.Text(), "hello")
			}
		}
	}

	// Turn boundary
	updatedModel, _ := m.Update(agentTurnDoneMsg{})
	m = updatedModel.(Model)

	// Turn 2: text — must use the SAME assistant item
	next, _ = m.handleAgentStreamMsg(agentStreamMsg{RunID: 7, Text: "world"}, nil)
	m = next
	id2 := m.currentAssistantID()

	if id1 != id2 {
		t.Fatalf("turn 2 should continue same assistant item (id1=%s id2=%s)", id1, id2)
	}

	// THE KEY ASSERTION: text accumulates across turns (continuous message)
	if item := m.chatList.FindByID(id2); item != nil {
		if a, ok := item.(*chat.AssistantItem); ok {
			if a.Text() != "helloworld" {
				t.Fatalf("continuous text = %q, want %q", a.Text(), "helloworld")
			}
		}
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

// TestEachLLMTurnContinuesSameAssistantItem verifies that after agentTurnDoneMsg,
// the next reasoning/text continues on the SAME assistant item (no fragmentation).
func TestEachLLMTurnContinuesSameAssistantItem(t *testing.T) {
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

	// streamPrefixWritten should STAY true so text continues in same item
	if !m.streamPrefixWritten {
		t.Fatal("streamPrefixWritten should remain true after agentTurnDoneMsg")
	}

	// Turn 2: new reasoning → must use SAME item
	next, _ = m.handleAgentReasoningMsg(agentReasoningMsg{RunID: 7, Text: "turn 2"}, nil)
	m = next
	id2 := m.currentAssistantID()

	if id1 != id2 {
		t.Fatalf("turn 2 should continue same assistant item (id1=%s id2=%s)", id1, id2)
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

	// agentTurnDoneMsg fires first (collapses reasoning)
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
