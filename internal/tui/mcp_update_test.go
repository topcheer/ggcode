package tui

import "testing"

func TestApplyMCPServersUpdateReconnectSettles(t *testing.T) {
	m := &Model{mcpPanel: &mcpPanelState{
		pendingReconnect: "http-server",
		message:          "Reconnecting http-server...",
	}}

	m.applyMCPServersUpdate(mcpServersUpdatedMsg{servers: []MCPInfo{
		{Name: "http-server", Pending: true},
	}})
	if got := m.mcpPanel.message; got != "Reconnecting http-server..." {
		t.Fatalf("pending state should keep the reconnecting message, got %q", got)
	}
	if m.mcpPanel.pendingReconnect != "http-server" {
		t.Fatalf("pendingReconnect must survive a pending update, got %q", m.mcpPanel.pendingReconnect)
	}

	m.applyMCPServersUpdate(mcpServersUpdatedMsg{servers: []MCPInfo{
		{Name: "http-server", Connected: true, ToolNames: []string{"a", "b", "c"}},
	}})
	if want := "Reconnected http-server: 3 tools."; m.mcpPanel.message != want {
		t.Fatalf("connected update: message = %q, want %q", m.mcpPanel.message, want)
	}
	if m.mcpPanel.pendingReconnect != "" {
		t.Fatalf("pendingReconnect must clear on success, got %q", m.mcpPanel.pendingReconnect)
	}
}

func TestApplyMCPServersUpdateReconnectFailure(t *testing.T) {
	m := &Model{mcpPanel: &mcpPanelState{
		pendingReconnect: "srv",
		message:          "Reconnecting srv...",
	}}
	m.applyMCPServersUpdate(mcpServersUpdatedMsg{servers: []MCPInfo{
		{Name: "srv", Error: "dial tcp: refused"},
	}})
	if want := "Reconnect failed for srv: dial tcp: refused"; m.mcpPanel.message != want {
		t.Fatalf("failed update: message = %q, want %q", m.mcpPanel.message, want)
	}
	if m.mcpPanel.pendingReconnect != "" {
		t.Fatalf("pendingReconnect must clear on failure, got %q", m.mcpPanel.pendingReconnect)
	}
}

func TestApplyMCPServersUpdateIgnoresOtherServers(t *testing.T) {
	m := &Model{mcpPanel: &mcpPanelState{pendingReconnect: "a"}}
	m.applyMCPServersUpdate(mcpServersUpdatedMsg{servers: []MCPInfo{
		{Name: "b", Connected: true},
	}})
	if m.mcpPanel.pendingReconnect != "a" {
		t.Fatalf("latch must survive unrelated server updates, got %q", m.mcpPanel.pendingReconnect)
	}

	m2 := &Model{mcpPanel: &mcpPanelState{pendingReconnect: "a"}, mcpServers: []MCPInfo{{Name: "a"}}}
	m2.applyMCPServersUpdate(mcpServersUpdatedMsg{servers: []MCPInfo{}})
	if m2.mcpPanel.pendingReconnect != "" {
		t.Fatalf("latch must clear when the pending server is removed, got %q", m2.mcpPanel.pendingReconnect)
	}
}

func TestApplyMCPServersUpdateWithoutPanel(t *testing.T) {
	m := &Model{}
	m.applyMCPServersUpdate(mcpServersUpdatedMsg{servers: []MCPInfo{
		{Name: "srv", Connected: true},
	}})
	if len(m.mcpServers) != 1 || !m.mcpServers[0].Connected {
		t.Fatalf("server list must update even without an open panel, got %+v", m.mcpServers)
	}
}

func TestApplyMCPServersUpdateConvergesShrunkSelection(t *testing.T) {
	m := &Model{mcpPanel: &mcpPanelState{selected: 3}}
	m.mcpServers = []MCPInfo{{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}}

	m.applyMCPServersUpdate(mcpServersUpdatedMsg{servers: []MCPInfo{
		{Name: "a"}, {Name: "b"},
	}})
	if got := m.mcpPanel.selected; got != 1 {
		t.Fatalf("selection must clamp to len-1 after shrink, got %d", got)
	}

	m.applyMCPServersUpdate(mcpServersUpdatedMsg{servers: nil})
	if got := m.mcpPanel.selected; got != 0 {
		t.Fatalf("selection must clamp to 0 on empty list, got %d", got)
	}

	m.mcpServers = []MCPInfo{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	m.mcpPanel.selected = 2
	m.SetMCPServers([]MCPInfo{{Name: "a"}})
	if got := m.mcpPanel.selected; got != 0 {
		t.Fatalf("SetMCPServers must converge selection, got %d", got)
	}
}
