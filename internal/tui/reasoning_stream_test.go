package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/chat"
)

func TestBuildBatchedStreamMessagesIncludesReasoningWithoutText(t *testing.T) {
	msgs := buildBatchedStreamMessages(7, "", "step 1", nil, nil)
	if len(msgs) != 1 {
		t.Fatalf("message count = %d, want 1", len(msgs))
	}

	reasoningMsg, ok := msgs[0].(agentReasoningMsg)
	if !ok {
		t.Fatalf("message type = %T, want agentReasoningMsg", msgs[0])
	}
	if reasoningMsg.RunID != 7 || reasoningMsg.Text != "step 1" {
		t.Fatalf("reasoning message = %+v, want run 7 with reasoning text", reasoningMsg)
	}
}

func TestBuildBatchedStreamMessagesOrdersReasoningThenTextThenTools(t *testing.T) {
	status := []agentStatusMsg{{RunID: 9, statusMsg: statusMsg{Activity: "Thinking..."}}}
	toolMsgs := []agentToolStatusMsg{{RunID: 9, ToolStatusMsg: ToolStatusMsg{ToolName: "bash"}}}

	msgs := buildBatchedStreamMessages(9, "answer", "thoughts", status, toolMsgs)
	if len(msgs) != 3 {
		t.Fatalf("message count = %d, want 3", len(msgs))
	}
	// Reasoning must come first so the TUI expands the thinking block before
	// the text chunk arrives and collapses it.
	if _, ok := msgs[0].(agentReasoningMsg); !ok {
		t.Fatalf("first message type = %T, want agentReasoningMsg", msgs[0])
	}
	if _, ok := msgs[1].(agentStreamMsg); !ok {
		t.Fatalf("second message type = %T, want agentStreamMsg", msgs[1])
	}
	if _, ok := msgs[2].(agentToolBatchMsg); !ok {
		t.Fatalf("third message type = %T, want agentToolBatchMsg", msgs[2])
	}
}

func TestHandleAgentReasoningMsgUsesAccumulatedReasoningText(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 3

	next, _ := m.handleAgentReasoningMsg(agentReasoningMsg{RunID: 3, Text: "first chunk"}, nil)
	m = next
	next, _ = m.handleAgentReasoningMsg(agentReasoningMsg{RunID: 3, Text: "first chunk second chunk"}, tea.Cmd(nil))
	m = next

	item := m.chatList.FindByID(m.currentAssistantID())
	if item == nil {
		t.Fatal("assistant item not found")
	}
	assistant, ok := item.(*chat.AssistantItem)
	if !ok {
		t.Fatalf("item type = %T, want *chat.AssistantItem", item)
	}
	if got := assistant.Reasoning(); got != "first chunk second chunk" {
		t.Fatalf("reasoning = %q, want accumulated reasoning text", got)
	}
}

// TestAgentTurnDoneMsgResetsStreamPrefix ensures that agentTurnDoneMsg
// (fired at StreamEventDone = LLM turn boundary) resets streamPrefixWritten
// so the next LLM turn creates a fresh assistant item. Each LLM turn gets
// its own assistant bubble, while text within a single turn stays unified
// even across tool calls.
func TestAgentTurnDoneMsgResetsStreamPrefix(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 5

	// Turn 1: reasoning arrives
	next, _ := m.handleAgentReasoningMsg(agentReasoningMsg{RunID: 5, Text: "turn 1 thinking"}, nil)
	m = next
	if !m.streamPrefixWritten {
		t.Fatal("streamPrefixWritten should be true after reasoning")
	}
	turn1ID := m.currentAssistantID()

	// End of turn 1 (StreamEventDone)
	updatedModel, _ := m.Update(agentTurnDoneMsg{})
	m = updatedModel.(Model)

	if m.streamPrefixWritten {
		t.Fatal("streamPrefixWritten should be false after agentTurnDoneMsg (turn boundary)")
	}

	// Turn 2: new reasoning arrives — should create a NEW assistant item
	next, _ = m.handleAgentReasoningMsg(agentReasoningMsg{RunID: 5, Text: "turn 2 thinking"}, nil)
	m = next
	turn2ID := m.currentAssistantID()

	if turn1ID == turn2ID {
		t.Fatal("turn 2 should get a different assistant ID than turn 1")
	}
}
