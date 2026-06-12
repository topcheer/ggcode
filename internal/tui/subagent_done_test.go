package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
)

func TestSubAgentDoneMsg_IdleAgent(t *testing.T) {
	// When the main agent is idle, subAgentDoneMsg should trigger a new agent loop.
	m := NewModel(nil, nil)
	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})

	msg := subAgentDoneMsg{
		AgentID:   "sa-1",
		AgentName: "code reviewer",
		IsError:   false,
		Kind:      "subagent",
	}

	next, cmd := m.Update(msg)
	m = next.(Model)
	if cmd == nil {
		t.Fatal("expected a tea.Cmd to be returned when agent is idle, got nil")
	}

	// Verify system message was written
	assertSystemMessage(t, &m, "completed", "code reviewer")
}

func TestSubAgentDoneMsg_BusyAgent(t *testing.T) {
	// When the main agent is busy, subAgentDoneMsg should queue the notification.
	m := NewModel(nil, nil)
	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})
	m.loading = true // simulate busy agent

	msg := subAgentDoneMsg{
		AgentID:   "sa-2",
		AgentName: "researcher",
		IsError:   false,
		Kind:      "subagent",
	}

	next, cmd := m.Update(msg)
	m = next.(Model)
	if cmd != nil {
		// tea.Cmd might return a no-op; verify it's not submitText by checking no run was started
		_ = cmd
	}

	// Verify pending submission was queued
	count := m.pendingSubmissionCount()
	if count != 1 {
		t.Fatalf("expected 1 pending submission, got %d", count)
	}

	// Verify system message was still written
	assertSystemMessage(t, &m, "completed", "researcher")
}

func TestSubAgentDoneMsg_BusyAgentSchedulesGraceCleanup(t *testing.T) {
	m := NewModel(nil, nil)
	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})
	m.loading = true

	id := m.subAgentMgr.Spawn("researcher", "researcher", "work", nil, context.Background())
	for _, sa := range m.subAgentMgr.List() {
		if sa.ID == id {
			sa.Status = subagent.StatusCompleted
			sa.EndedAt = time.Now()
		}
	}

	next, cmd := m.Update(subAgentDoneMsg{
		AgentID:   id,
		AgentName: "researcher",
		Kind:      "subagent",
	})
	m = next.(Model)

	if cmd == nil {
		t.Fatal("expected grace cleanup tick when a terminal subagent remains visible")
	}
	if count := m.pendingSubmissionCount(); count != 1 {
		t.Fatalf("expected queued follow-up while busy, got %d", count)
	}
}

func TestSubAgentDoneMsg_Error(t *testing.T) {
	// When a sub-agent fails, the notice should say "failed".
	m := NewModel(nil, nil)
	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})

	msg := subAgentDoneMsg{
		AgentID:   "sa-3",
		AgentName: "test agent",
		IsError:   true,
		Kind:      "subagent",
	}

	next, _ := m.Update(msg)
	m = next.(Model)

	assertSystemMessage(t, &m, "failed", "test agent")
}

func TestSubAgentDoneMsg_EmptyName(t *testing.T) {
	// When AgentName is empty, should fall back to AgentID.
	m := NewModel(nil, nil)
	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})

	msg := subAgentDoneMsg{
		AgentID:   "sa-42",
		AgentName: "",
		IsError:   false,
		Kind:      "teammate",
	}

	next, _ := m.Update(msg)
	m = next.(Model)

	assertSystemMessage(t, &m, "sa-42", "completed")
}

func TestSubAgentDoneMsg_MultipleDone(t *testing.T) {
	// Multiple sub-agents completing while busy should queue multiple pending submissions.
	m := NewModel(nil, nil)
	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})
	m.loading = true

	names := []string{"agent-a", "agent-b", "agent-c"}
	for _, name := range names {
		msg := subAgentDoneMsg{
			AgentID:   "sa-" + name,
			AgentName: name,
			IsError:   false,
			Kind:      "subagent",
		}
		next, _ := m.Update(msg)
		m = next.(Model)
	}

	count := m.pendingSubmissionCount()
	if count != 3 {
		t.Fatalf("expected 3 pending submissions, got %d", count)
	}
}

func TestFormatSubAgentDoneNotice(t *testing.T) {
	m := NewModel(nil, nil)

	tests := []struct {
		name     string
		msg      subAgentDoneMsg
		expected string
	}{
		{
			name:     "success with name",
			msg:      subAgentDoneMsg{AgentName: "reviewer", IsError: false},
			expected: "reviewer completed",
		},
		{
			name:     "error with name",
			msg:      subAgentDoneMsg{AgentName: "coder", IsError: true},
			expected: "coder failed",
		},
		{
			name:     "empty name falls back to ID",
			msg:      subAgentDoneMsg{AgentID: "sa-99", AgentName: "", IsError: false},
			expected: "sa-99 completed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.formatSubAgentDoneNotice(tt.msg)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestSubAgentDoneMsg_IdleStartsNewRun(t *testing.T) {
	// Verify that when idle, the cmd returned triggers startAgent behavior.
	m := NewModel(nil, nil)
	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})

	msg := subAgentDoneMsg{
		AgentID:   "sa-review",
		AgentName: "reviewer",
		IsError:   false,
		Kind:      "subagent",
	}

	_, cmd := m.Update(msg)

	// The cmd should produce a message (it wraps submitText which calls startAgent).
	// We can't easily execute the cmd in a test without a full tea.Program,
	// but we can verify it's not nil and produces a batch.
	if cmd == nil {
		t.Fatal("expected non-nil cmd when agent is idle after subAgentDoneMsg")
	}

	// Execute the cmd to verify it doesn't panic
	msg2 := cmd()
	_ = msg2
}

// assertSystemMessage checks that a system message containing all expected substrings exists.
func assertSystemMessage(t *testing.T, m *Model, expected ...string) {
	t.Helper()
	for i := 0; i < m.chatList.Len(); i++ {
		item := m.chatList.ItemAt(i)
		rendered := ""
		if r, ok := item.(interface{ Render(int) string }); ok {
			rendered = r.Render(120)
		}
		allMatch := true
		for _, exp := range expected {
			if !strings.Contains(rendered, exp) {
				allMatch = false
				break
			}
		}
		if allMatch && rendered != "" {
			return
		}
	}
	t.Errorf("expected system message containing %v in chat list items", expected)
}
