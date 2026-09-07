package tui

import "testing"

// applyMCPServersUpdate must upgrade the panel's static "reconnecting"
// message to a terminal result as soon as the pending server settles, and
// must keep it while the server is still dialing or awaiting OAuth.
func TestApplyMCPServersUpdateReconnectSettles(t *testing.T) {
	m := &Model{mcpPanel: &mcpPanelState{
		pendingReconnect: "http-server",
		message:          "Reconnecting http-server...",
	}}

	// Still dialing / awaiting OAuth: message stays.
	m.applyMCPServersUpdate(mcpServersUpdatedMsg{servers: []MCPInfo{
		{Name: "http-server", Pending: true},
	}})
	if got := m.mcpPanel.message; got != "Reconnecting http-server..." {
		t.Fatalf("pending state should keep the reconnecting message, got %q", got)
	}
	if m.mcpPanel.pendingReconnect != "http-server" {
		t.Fatalf("pendingReconnect must survive a pending update, got %q", m.mcpPanel.pendingReconnect)
	}

	// Connected: message upgrades and the latch clears.
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

// Other servers settling must not consume the latch.
func TestApplyMCPServersUpdateIgnoresOtherServers(t *testing.T) {
	m := &Model{mcpPanel: &mcpPanelState{pendingReconnect: "a"}}
	m.applyMCPServersUpdate(mcpServersUpdatedMsg{servers: []MCPInfo{
		{Name: "b", Connected: true},
	}})
	if m.mcpPanel.pendingReconnect != "a" {
		t.Fatalf("latch must survive unrelated server updates, got %q", m.mcpPanel.pendingReconnect)
	}
}

// With no panel open the fresh list still lands in m.mcpServers.
func TestApplyMCPServersUpdateWithoutPanel(t *testing.T) {
	m := &Model{}
	m.applyMCPServersUpdate(mcpServersUpdatedMsg{servers: []MCPInfo{
		{Name: "srv", Connected: true},
	}})
	if len(m.mcpServers) != 1 || !m.mcpServers[0].Connected {
		t.Fatalf("server list must update even without an open panel, got %+v", m.mcpServers)
	}
}
