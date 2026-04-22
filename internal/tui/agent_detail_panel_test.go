package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
)

// --- Open / Close ---

func TestAgentDetailPanelOpenSetsState(t *testing.T) {
	m := NewModel(nil, nil)
	m.openAgentDetailPanel("sa-1")
	if m.agentDetailPanel == nil {
		t.Fatal("expected agentDetailPanel to be set")
	}
	if m.agentDetailPanel.agentID != "sa-1" {
		t.Fatalf("expected agentID sa-1, got %s", m.agentDetailPanel.agentID)
	}
}

func TestAgentDetailPanelCloseNilState(t *testing.T) {
	m := NewModel(nil, nil)
	m.openAgentDetailPanel("sa-1")
	m.closeAgentDetailPanel()
	if m.agentDetailPanel != nil {
		t.Fatal("expected agentDetailPanel to be nil after close")
	}
}

func TestAgentDetailPanelCloseActivePanel(t *testing.T) {
	m := newTestModel()
	m.openAgentDetailPanel("sa-1")
	if !m.closeActivePanel() {
		t.Fatal("expected closeActivePanel to return true")
	}
	if m.agentDetailPanel != nil {
		t.Fatal("expected closeActivePanel to close agentDetailPanel")
	}
}

// --- Keyboard ---

func TestAgentDetailPanelEscCloses(t *testing.T) {
	m := NewModel(nil, nil)
	m.openAgentDetailPanel("sa-1")
	updated, cmd := m.handleAgentDetailPanelKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("expected no command on esc")
	}
	if updated.agentDetailPanel != nil {
		t.Fatal("expected esc to close panel")
	}
}

func TestAgentDetailPanelQCloses(t *testing.T) {
	m := NewModel(nil, nil)
	m.openAgentDetailPanel("sa-1")
	updated, _ := m.handleAgentDetailPanelKey(tea.KeyPressMsg{Text: "q"})
	if updated.agentDetailPanel != nil {
		t.Fatal("expected q to close panel")
	}
}

func TestAgentDetailPanelJScrollsDown(t *testing.T) {
	m := NewModel(nil, nil)
	m.openAgentDetailPanel("sa-1")
	updated, _ := m.handleAgentDetailPanelKey(tea.KeyPressMsg{Text: "j"})
	if updated.agentDetailPanel.scrollY != 1 {
		t.Fatalf("expected scrollY=1, got %d", updated.agentDetailPanel.scrollY)
	}
}

func TestAgentDetailPanelKScrollsUp(t *testing.T) {
	m := NewModel(nil, nil)
	m.openAgentDetailPanel("sa-1")
	m.agentDetailPanel.scrollY = 3
	updated, _ := m.handleAgentDetailPanelKey(tea.KeyPressMsg{Text: "k"})
	if updated.agentDetailPanel.scrollY != 2 {
		t.Fatalf("expected scrollY=2, got %d", updated.agentDetailPanel.scrollY)
	}
}

func TestAgentDetailPanelKStopsAtZero(t *testing.T) {
	m := NewModel(nil, nil)
	m.openAgentDetailPanel("sa-1")
	m.agentDetailPanel.scrollY = 0
	updated, _ := m.handleAgentDetailPanelKey(tea.KeyPressMsg{Text: "k"})
	if updated.agentDetailPanel.scrollY != 0 {
		t.Fatalf("expected scrollY to stay at 0, got %d", updated.agentDetailPanel.scrollY)
	}
}

func TestAgentDetailPanelGotoTop(t *testing.T) {
	m := NewModel(nil, nil)
	m.openAgentDetailPanel("sa-1")
	m.agentDetailPanel.scrollY = 50
	updated, _ := m.handleAgentDetailPanelKey(tea.KeyPressMsg{Text: "g"})
	if updated.agentDetailPanel.scrollY != 0 {
		t.Fatalf("expected scrollY=0 after g, got %d", updated.agentDetailPanel.scrollY)
	}
}

func TestAgentDetailPanelGotoBottom(t *testing.T) {
	m := NewModel(nil, nil)
	m.openAgentDetailPanel("sa-1")
	updated, _ := m.handleAgentDetailPanelKey(tea.KeyPressMsg{Text: "G"})
	if updated.agentDetailPanel.scrollY != 99999 {
		t.Fatalf("expected scrollY=99999 after G, got %d", updated.agentDetailPanel.scrollY)
	}
}

// --- Render ---

func TestAgentDetailPanelRenderNil(t *testing.T) {
	m := NewModel(nil, nil)
	rendered := m.renderAgentDetailPanel()
	if rendered != "" {
		t.Fatalf("expected empty render when panel is nil, got: %s", rendered)
	}
}

func TestAgentDetailPanelRenderNotFound(t *testing.T) {
	m := NewModel(nil, nil)
	mgr := subagent.NewManager(config.SubAgentConfig{})
	m.subAgentMgr = mgr
	m.openAgentDetailPanel("sa-999")
	rendered := m.renderAgentDetailPanel()
	// Agent not found: returns empty or error message
	// With subAgentMgr set but agent missing, we get a "not found" message
	if rendered == "" {
		t.Fatal("expected error message for missing agent")
	}
}

func helperSpawnAgentWithEvents(mgr *subagent.Manager, task string, events []subagent.AgentEvent) string {
	id := mgr.Spawn(task, task, nil, nil)
	sa, _ := mgr.Get(id)
	for _, ev := range events {
		sa.RecordEvent(ev)
	}
	return id
}

func TestAgentDetailPanelRenderWithEvents(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40

	mgr := subagent.NewManager(config.SubAgentConfig{})
	m.subAgentMgr = mgr

	id := helperSpawnAgentWithEvents(mgr, "test task", []subagent.AgentEvent{
		{Type: subagent.AgentEventText, Text: "thinking about code"},
		{Type: subagent.AgentEventToolCall, ToolName: "read_file", ToolArgs: `{"path":"/tmp/test.go"}`},
		{Type: subagent.AgentEventToolResult, ToolName: "read_file", Result: "package main", IsError: false},
	})

	m.openAgentDetailPanel(id)
	rendered := m.renderAgentDetailPanel()
	if rendered == "" {
		t.Fatal("expected non-empty render")
	}
	if !strings.Contains(rendered, "test task") {
		t.Fatalf("expected task name in render, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "read_file") {
		t.Fatalf("expected tool call in render, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "thinking about code") {
		t.Fatalf("expected text event in render, got:\n%s", rendered)
	}
}

func TestAgentDetailPanelRenderErrorEvent(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40

	mgr := subagent.NewManager(config.SubAgentConfig{})
	m.subAgentMgr = mgr

	id := helperSpawnAgentWithEvents(mgr, "task", []subagent.AgentEvent{
		{Type: subagent.AgentEventError, Text: "connection refused", IsError: true},
	})

	m.openAgentDetailPanel(id)
	rendered := m.renderAgentDetailPanel()
	if !strings.Contains(rendered, "✗") {
		t.Fatalf("expected error symbol in render, got:\n%s", rendered)
	}
}

func TestAgentDetailPanelRenderEmptyEvents(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40

	mgr := subagent.NewManager(config.SubAgentConfig{})
	m.subAgentMgr = mgr

	id := mgr.Spawn("task", "task", nil, nil)
	m.openAgentDetailPanel(id)
	rendered := m.renderAgentDetailPanel()
	if !strings.Contains(rendered, "Waiting for events") {
		t.Fatalf("expected 'Waiting for events' message, got:\n%s", rendered)
	}
}

func TestAgentDetailPanelRenderedInContextPanel(t *testing.T) {
	m := newTestModel()
	m.width = 120
	m.height = 40

	mgr := subagent.NewManager(config.SubAgentConfig{})
	m.subAgentMgr = mgr

	id := mgr.Spawn("task", "task", nil, nil)
	m.openAgentDetailPanel(id)
	panel := m.renderContextPanel()
	if panel == "" {
		t.Fatal("expected context panel to render agent detail panel")
	}
}

func TestAgentDetailPanelStatusColorRunning(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40

	mgr := subagent.NewManager(config.SubAgentConfig{})
	m.subAgentMgr = mgr

	id := mgr.Spawn("task", "task", nil, nil)
	mgr.SetCancel(id, func() {}) // sets status to running

	m.openAgentDetailPanel(id)
	rendered := m.renderAgentDetailPanel()
	if !strings.Contains(rendered, "running") {
		t.Fatalf("expected 'running' in render, got:\n%s", rendered)
	}
}

// --- Integration: /agent command wiring ---

func TestAgentCommandOpensDetailPanel(t *testing.T) {
	m := newTestModel()
	m.width = 120
	m.height = 40

	mgr := subagent.NewManager(config.SubAgentConfig{})
	m.subAgentMgr = mgr

	id := mgr.Spawn("test task", "display task", nil, nil)

	m.handleAgentDetailCommand([]string{"agent", id})
	if m.agentDetailPanel == nil {
		t.Fatal("expected /agent <id> to open detail panel")
	}
	if m.agentDetailPanel.agentID != id {
		t.Fatalf("expected panel agentID=%s, got %s", id, m.agentDetailPanel.agentID)
	}
}

func TestAgentCommandNoArgOpensInspector(t *testing.T) {
	m := newTestModel()
	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})

	m.handleAgentDetailCommand([]string{"agent"})
	if m.inspectorPanel == nil {
		t.Fatal("expected /agent (no arg) to open inspector panel")
	}
	if m.inspectorPanel.kind != inspectorPanelAgents {
		t.Fatal("expected inspector panel to show agents tab")
	}
}

func TestAgentCommandNotFoundShowsError(t *testing.T) {
	m := newTestModel()
	m.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})

	m.handleAgentDetailCommand([]string{"agent", "sa-999"})
	if m.agentDetailPanel != nil {
		t.Fatal("expected no panel for missing agent")
	}
	output := m.output.String()
	if !strings.Contains(output, "sa-999") {
		t.Fatalf("expected error message with agent ID, got: %s", output)
	}
}

func TestAgentCommandCancelStillWorks(t *testing.T) {
	m := newTestModel()
	mgr := subagent.NewManager(config.SubAgentConfig{})
	m.subAgentMgr = mgr

	id := mgr.Spawn("task", "task", nil, nil)
	mgr.SetCancel(id, func() {}) // sets status to running

	m.handleAgentDetailCommand([]string{"agent", "cancel", id})
	if m.agentDetailPanel != nil {
		t.Fatal("expected cancel to not open panel")
	}
	output := m.output.String()
	if !strings.Contains(output, id) {
		t.Fatalf("expected cancel output with agent ID, got: %s", output)
	}
}
