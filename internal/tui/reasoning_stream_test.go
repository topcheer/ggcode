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

func TestBuildBatchedStreamMessagesOrdersTextThenReasoningThenTools(t *testing.T) {
	status := []agentStatusMsg{{RunID: 9, statusMsg: statusMsg{Activity: "Thinking..."}}}
	toolMsgs := []agentToolStatusMsg{{RunID: 9, ToolStatusMsg: ToolStatusMsg{ToolName: "bash"}}}

	msgs := buildBatchedStreamMessages(9, "answer", "thoughts", status, toolMsgs)
	if len(msgs) != 3 {
		t.Fatalf("message count = %d, want 3", len(msgs))
	}
	if _, ok := msgs[0].(agentStreamMsg); !ok {
		t.Fatalf("first message type = %T, want agentStreamMsg", msgs[0])
	}
	if _, ok := msgs[1].(agentReasoningMsg); !ok {
		t.Fatalf("second message type = %T, want agentReasoningMsg", msgs[1])
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

// TestReasoningDoneMsgResetsStreamPrefixWritten ensures that after
// agentReasoningDoneMsg fires (end of an LLM turn), streamPrefixWritten is
// reset so the next turn creates a fresh assistant bubble instead of
// appending to the collapsed reasoning block. This is a regression test for
// the "duplicate reasoning across turns" bug where turn N+1's reasoning was
// prepended to turn N's, producing a superset effect.
func TestReasoningDoneMsgResetsStreamPrefixWritten(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 5

	// Simulate reasoning arriving for turn 1
	next, _ := m.handleAgentReasoningMsg(agentReasoningMsg{RunID: 5, Text: "turn 1 thinking"}, nil)
	m = next

	if !m.streamPrefixWritten {
		t.Fatal("streamPrefixWritten should be true after reasoning arrives")
	}

	// Simulate end of turn 1
	updatedModel, _ := m.Update(agentReasoningDoneMsg{})
	m = updatedModel.(Model)

	if m.streamPrefixWritten {
		t.Fatal("streamPrefixWritten should be false after agentReasoningDoneMsg")
	}
}
