package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/chat"
)

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

func TestReasoningCollapsesOnFirstTextChunk(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 7

	next, _ := m.handleAgentReasoningMsg(agentReasoningMsg{RunID: 7, Text: "thinking..."}, nil)
	m = next
	if !m.reasoningActive {
		t.Fatal("reasoningActive should be true after reasoning chunk")
	}

	next, _ = m.handleAgentStreamMsg(agentStreamMsg{RunID: 7, Text: "hello"}, nil)
	m = next

	if m.reasoningActive {
		t.Fatal("reasoningActive should be false after first text chunk (reasoning must collapse)")
	}
}

func TestReasoningCollapsesOnFirstToolEvent(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 7

	next, _ := m.handleAgentReasoningMsg(agentReasoningMsg{RunID: 7, Text: "I need to read a file"}, nil)
	m = next
	if !m.reasoningActive {
		t.Fatal("reasoningActive should be true after reasoning chunk")
	}

	ts := toolStatusMsg{ToolName: "read_file", Detail: "main.go", Running: true}
	next, _ = m.handleToolStatusMsg(ts, nil)
	m = next

	if m.reasoningActive {
		t.Fatal("reasoningActive should be false after first tool event (reasoning must collapse)")
	}
}

func TestTextDoesNotFragmentAcrossToolResults(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 7

	next, _ := m.handleAgentStreamMsg(agentStreamMsg{RunID: 7, Text: "before tool"}, nil)
	m = next
	idBefore := m.currentAssistantID()

	ts := toolStatusMsg{ToolName: "read_file", Detail: "test.go", Running: false}
	next, _ = m.handleToolStatusMsg(ts, nil)
	m = next

	next, _ = m.handleAgentStreamMsg(agentStreamMsg{RunID: 7, Text: "after tool"}, nil)
	m = next
	idAfter := m.currentAssistantID()

	if idBefore != idAfter {
		t.Fatalf("text fragmented: idBefore=%s idAfter=%s (should be same item within one turn)", idBefore, idAfter)
	}
}

func TestToolResultDoesNotResetStreamPrefix(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 7

	next, _ := m.handleAgentReasoningMsg(agentReasoningMsg{RunID: 7, Text: "thinking"}, nil)
	m = next

	ts := toolStatusMsg{ToolName: "grep", Detail: "*.go", Running: false}
	next, _ = m.handleToolStatusMsg(ts, nil)
	m = next

	if !m.streamPrefixWritten {
		t.Fatal("streamPrefixWritten must stay true after tool result (prevents text fragmentation)")
	}
}

func TestTextDoesNotAccumulateAcrossTurns(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 7

	next, _ := m.handleAgentStreamMsg(agentStreamMsg{RunID: 7, Text: "hello"}, nil)
	m = next
	id1 := m.currentAssistantID()

	if item := m.chatList.FindByID(id1); item != nil {
		if a, ok := item.(*chat.AssistantItem); ok {
			if a.Text() != "hello" {
				t.Fatalf("turn 1 text = %q, want %q", a.Text(), "hello")
			}
		}
	}

	updatedModel, _ := m.Update(agentTurnDoneMsg{})
	m = updatedModel.(Model)

	next, _ = m.handleAgentStreamMsg(agentStreamMsg{RunID: 7, Text: "world"}, nil)
	m = next
	id2 := m.currentAssistantID()

	if id1 == id2 {
		t.Fatal("turn 2 should create a new assistant item")
	}

	if item := m.chatList.FindByID(id2); item != nil {
		if a, ok := item.(*chat.AssistantItem); ok {
			if a.Text() != "world" {
				t.Fatalf("TEXT ACCUMULATION BUG: turn 2 text = %q, want %q (streamBuffer was not reset at turn boundary)", a.Text(), "world")
			}
		}
	}
}

func TestChatFinishReasoningIdempotent(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 7
	m.reasoningActive = false

	m.chatFinishReasoning()

	if m.reasoningActive {
		t.Fatal("chatFinishReasoning should be no-op when reasoning not active")
	}
}

func TestEachLLMTurnGetsSeparateAssistantItem(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 7

	next, _ := m.handleAgentReasoningMsg(agentReasoningMsg{RunID: 7, Text: "turn 1"}, nil)
	m = next
	id1 := m.currentAssistantID()

	updatedModel, _ := m.Update(agentTurnDoneMsg{})
	m = updatedModel.(Model)

	if m.streamPrefixWritten {
		t.Fatal("streamPrefixWritten should be false after agentTurnDoneMsg")
	}
	if m.streamBuffer != nil && m.streamBuffer.Len() != 0 {
		t.Fatalf("streamBuffer should be empty after agentTurnDoneMsg, got %d bytes", m.streamBuffer.Len())
	}

	next, _ = m.handleAgentReasoningMsg(agentReasoningMsg{RunID: 7, Text: "turn 2"}, nil)
	m = next
	id2 := m.currentAssistantID()

	if id1 == id2 {
		t.Fatalf("turn 2 should create new assistant item (id1=%s id2=%s)", id1, id2)
	}
}

func TestAgentDoneFinalizesEverything(t *testing.T) {
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 7

	next, _ := m.handleAgentReasoningMsg(agentReasoningMsg{RunID: 7, Text: "thinking"}, nil)
	m = next
	m.handleAgentStreamMsg(agentStreamMsg{RunID: 7, Text: "result"}, nil)

	updatedModel, _ := m.Update(agentTurnDoneMsg{})
	m = updatedModel.(Model)

	updatedModel, _ = m.Update(agentDoneMsg{RunID: 7})
	m = updatedModel.(Model)

	if m.reasoningActive {
		t.Fatal("reasoningActive should be false after turn-done + agent-done")
	}
	if m.loading {
		t.Fatal("loading should be false after agentDoneMsg")
	}
}
