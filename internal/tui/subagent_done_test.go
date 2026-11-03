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

	assertSystemMessage(t, &m, "completed", "code reviewer")
}

func TestSubAgentDoneMsg_BusyAgent(t *testing.T) {

	m := NewModel(nil, nil)
	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})
	m.loading = true

	msg := subAgentDoneMsg{
		AgentID:   "sa-2",
		AgentName: "researcher",
		IsError:   false,
		Kind:      "subagent",
	}

	next, _ := m.Update(msg)
	m = next.(Model)

	count := m.pendingSubmissionCount()
	if count != 0 {
		t.Fatalf("expected 0 pending submissions when busy, got %d", count)
	}

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
	if count := m.pendingSubmissionCount(); count != 0 {
		t.Fatalf("expected 0 pending submissions when busy, got %d", count)
	}
}

func TestSubAgentDoneMsg_Error(t *testing.T) {

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
	if count != 0 {
		t.Fatalf("expected 0 pending submissions when busy, got %d", count)
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

	m := NewModel(nil, nil)
	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})

	msg := subAgentDoneMsg{
		AgentID:   "sa-review",
		AgentName: "reviewer",
		IsError:   false,
		Kind:      "subagent",
	}

	_, cmd := m.Update(msg)

	if cmd == nil {
		t.Fatal("expected non-nil cmd when agent is idle after subAgentDoneMsg")
	}

	msg2 := cmd()
	_ = msg2
}

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
