package tui

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/chat"
)

// TestChatListFullFlow verifies the complete message lifecycle:
// user msg → tool start → streaming → tool finish → done
func TestChatListFullFlow(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)
	m.loading = true

	// 1. User message
	m.chatWriteUser("u1", "hello")

	// 2. Assistant starts streaming
	aid := m.currentAssistantID()
	m.chatEnsureAssistant()

	// 3. Streaming text
	item := m.chatList.FindByID(aid)
	ai := item.(*chat.AssistantItem)
	ai.SetText("I will read a file for you.")
	ai.Invalidate()

	// 4. Tool starts
	m.chatStartTool(ToolStatusMsg{
		ToolID:   "tool-1",
		ToolName: "read_file",
		Detail:   "main.go",
		Running:  true,
	})

	// 5. Tool finishes
	m.chatFinishTool(ToolStatusMsg{
		ToolID:   "tool-1",
		ToolName: "read_file",
		Running:  false,
		Result:   "package main\nfunc main() {}",
	})

	// 6. More streaming
	ai.SetText("I will read a file for you.\nHere is the content of main.go.")
	ai.Invalidate()

	// 7. Finish assistant
	m.chatFinishAssistant(aid)

	// Verify chatList contents
	if m.chatList.Len() != 3 {
		t.Fatalf("expected 3 items (user, assistant, tool), got %d", m.chatList.Len())
	}

	// Item 0: user
	u := m.chatList.ItemAt(0)
	rendered := stripAnsiCodes(u.Render(100))
	if !strings.Contains(rendered, "hello") {
		t.Errorf("user item missing 'hello': %q", rendered)
	}

	// Item 1: assistant
	a := m.chatList.ItemAt(1)
	rendered = stripAnsiCodes(a.Render(100))
	if !strings.Contains(rendered, "main.go") {
		t.Errorf("assistant item missing 'main.go': %q", rendered)
	}

	// Item 2: tool
	tl := m.chatList.ItemAt(2)
	rendered = stripAnsiCodes(tl.Render(100))
	if !strings.Contains(rendered, "main.go") {
		t.Errorf("tool item missing 'main.go': %q", rendered)
	}
	if strings.Contains(rendered, "⏳") {
		t.Errorf("tool should be finished, not running: %q", rendered)
	}
	if !strings.Contains(rendered, "✓") {
		t.Errorf("tool should show success icon: %q", rendered)
	}

	// Verify Render() output
	m.chatList.SetSize(100, 30)
	output := m.chatList.Render()
	clean := stripAnsiCodes(output)
	if !strings.Contains(clean, "hello") {
		t.Error("rendered output missing user message")
	}
	if !strings.Contains(clean, "main.go") {
		t.Error("rendered output missing tool info")
	}
}

// TestChatListSystemMessages verifies system messages appear in chatList.
func TestChatListSystemMessages(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)

	// Write a system message via dualWriteSystem
	m.dualWriteSystem("Model switched to gpt-4o")

	if m.chatList.Len() != 1 {
		t.Fatalf("expected 1 system item, got %d", m.chatList.Len())
	}

	item := m.chatList.ItemAt(0)
	rendered := stripAnsiCodes(item.Render(100))
	if !strings.Contains(rendered, "Model switched to gpt-4o") {
		t.Errorf("system item missing message: %q", rendered)
	}
}

// TestChatListNoDuplicateUserMessages verifies user messages don't appear twice.
func TestChatListNoDuplicateUserMessages(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)

	// Simulate what handleCommand does
	m.chatWriteUser(nextChatID(), "hello")

	// Should only have 1 user item
	count := 0
	for i := 0; i < m.chatList.Len(); i++ {
		item := m.chatList.ItemAt(i)
		if _, ok := item.(*chat.UserItem); ok {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 user item, got %d", count)
	}
}

// TestChatListAgentToolItem verifies agent lifecycle.
func TestChatListAgentToolItem(t *testing.T) {
	m := newTestModel()
	m.handleResize(120, 40)

	// spawn_agent
	m.chatStartTool(ToolStatusMsg{
		ToolID:   "agent-1",
		ToolName: "spawn_agent",
		Detail:   "fix the bug",
		Running:  true,
	})

	if m.chatList.Len() != 1 {
		t.Fatalf("expected 1 item after spawn_agent, got %d", m.chatList.Len())
	}

	agent := m.chatList.ItemAt(0)
	if _, ok := agent.(*chat.AgentToolItem); !ok {
		t.Fatalf("expected AgentToolItem, got %T", agent)
	}

	rendered := stripAnsiCodes(agent.Render(100))
	if !strings.Contains(rendered, "Agent") {
		t.Errorf("agent item missing 'Agent': %q", rendered)
	}

	// wait_agent finishes
	m.chatFinishTool(ToolStatusMsg{
		ToolID:   "agent-1",
		ToolName: "spawn_agent",
		Running:  false,
		Result:   "Task completed successfully",
	})

	rendered = stripAnsiCodes(agent.Render(100))
	if !strings.Contains(rendered, "✓") {
		t.Errorf("agent should show success after finish: %q", rendered)
	}
}

func stripAnsiCodes(s string) string {
	var result strings.Builder
	inEscape := false
	for _, c := range s {
		if c == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if c == 'm' {
				inEscape = false
			}
			continue
		}
		result.WriteRune(c)
	}
	return result.String()
}
