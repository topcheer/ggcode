package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/swarm"
)

func defaultSwarmCfg() config.SwarmConfig {
	return config.SwarmConfig{
		MaxTeammatesPerTeam: 5,
		TeammateTimeout:     30 * time.Minute,
		InboxSize:           32,
	}
}

func TestSwarmPanelEscClosesPanel(t *testing.T) {
	m := NewModel(nil, nil)
	m.openSwarmPanel()
	if m.swarmPanel == nil {
		t.Fatal("expected swarm panel to be open")
	}
	updated, cmd := m.handleSwarmPanelKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("expected esc panel close without command")
	}
	m2 := updated.(*Model)
	if m2.swarmPanel != nil {
		t.Fatal("expected esc to close the swarm panel")
	}
}

func TestSwarmPanelQClosesPanel(t *testing.T) {
	m := NewModel(nil, nil)
	m.openSwarmPanel()
	updated, _ := m.handleSwarmPanelKey(tea.KeyPressMsg{Text: "q"})
	m2 := updated.(*Model)
	if m2.swarmPanel != nil {
		t.Fatal("expected q to close the swarm panel")
	}
}

func TestSwarmPanelJMovesCursorDown(t *testing.T) {
	m := NewModel(nil, nil)
	m.openSwarmPanel()
	initial := m.swarmPanel.cursor
	updated, _ := m.handleSwarmPanelKey(tea.KeyPressMsg{Text: "j"})
	m2 := updated.(*Model)
	if m2.swarmPanel.cursor != initial+1 {
		t.Fatalf("expected cursor to move down, got %d", m2.swarmPanel.cursor)
	}
}

func TestSwarmPanelKMovesCursorUp(t *testing.T) {
	m := NewModel(nil, nil)
	m.openSwarmPanel()
	m.swarmPanel.cursor = 3
	updated, _ := m.handleSwarmPanelKey(tea.KeyPressMsg{Text: "k"})
	m2 := updated.(*Model)
	if m2.swarmPanel.cursor != 2 {
		t.Fatalf("expected cursor to move up to 2, got %d", m2.swarmPanel.cursor)
	}
}

func TestSwarmPanelKStopsAtZero(t *testing.T) {
	m := NewModel(nil, nil)
	m.openSwarmPanel()
	m.swarmPanel.cursor = 0
	updated, _ := m.handleSwarmPanelKey(tea.KeyPressMsg{Text: "k"})
	m2 := updated.(*Model)
	if m2.swarmPanel.cursor != 0 {
		t.Fatalf("expected cursor to stay at 0, got %d", m2.swarmPanel.cursor)
	}
}

func TestSwarmPanelRenderNoTeams(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40
	m.swarmMgr = swarm.NewManager(defaultSwarmCfg(), nil, nil, nil)
	m.openSwarmPanel()
	rendered := m.renderSwarmPanel()
	if !strings.Contains(rendered, "No teams") {
		t.Fatalf("expected 'No teams' message, got:\n%s", rendered)
	}
}

func TestSwarmPanelRenderWithTeams(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40

	mgr := swarm.NewManager(defaultSwarmCfg(), nil, nil, nil)
	m.swarmMgr = mgr

	mgr.CreateTeam("test-team", "leader-1")

	m.openSwarmPanel()
	rendered := m.renderSwarmPanel()
	if !strings.Contains(rendered, "test-team") {
		t.Fatalf("expected team name in panel, got:\n%s", rendered)
	}
	mgr.Shutdown()
}

func TestSwarmPanelRenderNil(t *testing.T) {
	m := NewModel(nil, nil)
	// swarmPanel is nil
	rendered := m.renderSwarmPanel()
	if rendered != "" {
		t.Fatalf("expected empty render when panel is nil, got: %s", rendered)
	}
}

func TestSwarmPanelRenderNilManager(t *testing.T) {
	m := NewModel(nil, nil)
	m.openSwarmPanel()
	// swarmMgr is nil
	rendered := m.renderSwarmPanel()
	if rendered != "" {
		t.Fatalf("expected empty render when swarmMgr is nil, got: %s", rendered)
	}
}

func TestSwarmPanelCloseActivePanel(t *testing.T) {
	m := NewModel(nil, nil)
	m.openSwarmPanel()
	if m.swarmPanel == nil {
		t.Fatal("expected swarm panel to be open")
	}
	closed := m.closeActivePanel()
	if !closed {
		t.Fatal("expected closeActivePanel to return true")
	}
	if m.swarmPanel != nil {
		t.Fatal("expected closeActivePanel to close swarm panel")
	}
}

func TestSwarmSidebarNoManager(t *testing.T) {
	m := NewModel(nil, nil)
	rendered := m.renderSwarmSidebar()
	if rendered != "" {
		t.Fatalf("expected empty sidebar when no swarmMgr, got: %s", rendered)
	}
}

func TestSwarmSidebarNoActiveTeammates(t *testing.T) {
	m := NewModel(nil, nil)
	m.width = 120
	m.height = 40

	mgr := swarm.NewManager(defaultSwarmCfg(), nil, nil, nil)
	m.swarmMgr = mgr

	// Create team but no teammates
	mgr.CreateTeam("empty-team", "leader-1")

	rendered := m.renderSwarmSidebar()
	if rendered != "" {
		t.Fatalf("expected empty sidebar with no teammates, got: %s", rendered)
	}
	mgr.Shutdown()
}

func TestSwarmStatusIcons(t *testing.T) {
	tests := []struct {
		status swarm.TeammateStatus
		icon   string
	}{
		{swarm.TeammateIdle, "○"},
		{swarm.TeammateWorking, "●"},
		{swarm.TeammateShuttingDown, "✕"},
		{swarm.TeammateStatus("unknown"), "?"},
	}
	for _, tt := range tests {
		got := swarmStatusIcon(tt.status)
		if got != tt.icon {
			t.Errorf("swarmStatusIcon(%v) = %q, want %q", tt.status, got, tt.icon)
		}
	}
}
