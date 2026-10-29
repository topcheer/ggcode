package tui

import (
	"os"
	"strings"
	"testing"
)

// TestMCPPanelRefreshRunsOffUIGoroutine guards the f-key hang fix: the MCP
// panel refresh must run MCPManager.Refresh inside an async tea.Cmd closure,
// never inline in Update. A wedged MCP server's tools/list RPC used to freeze
// the whole TUI because Refresh was a synchronous call on the UI goroutine.
func TestMCPPanelRefreshRunsOffUIGoroutine(t *testing.T) {
	src, err := os.ReadFile("mcp_panel.go")
	if err != nil {
		t.Fatalf("read mcp_panel.go: %v", err)
	}
	text := string(src)

	// The handler must return a Cmd (async dispatch), not mutate state only.
	if !strings.Contains(text, "func (m *Model) mcpPanelRefresh(panel *mcpPanelState) tea.Cmd") {
		t.Fatal("mcpPanelRefresh must return tea.Cmd so the RPC runs off the UI goroutine")
	}
	// The blocking RPC must run inside the closure, on the captured manager.
	if !strings.Contains(text, "found, outcome, count := mgr.Refresh(name)") {
		t.Fatal("Refresh must be invoked inside the async closure via the captured mgr")
	}
	// The old synchronous inline call must not resurface.
	if strings.Contains(text, "found, outcome, count := m.mcpManager.Refresh(name)") {
		t.Fatal("synchronous MCPManager.Refresh call in Update path regressed (TUI freeze)")
	}
	// The key handler must propagate the returned Cmd.
	if !strings.Contains(text, "if cmd := m.mcpPanelRefresh(panel); cmd != nil") {
		t.Fatal("handleMCPPanelKey f-case must return the refresh tea.Cmd")
	}
}

// TestMCPPanelRefreshResultDispatched asserts the async result message is
// wired into the update dispatcher - without registration the panel message
// would silently never update after refresh completes.
func TestMCPPanelRefreshResultDispatched(t *testing.T) {
	src, err := os.ReadFile("model_update_dispatch.go")
	if err != nil {
		t.Fatalf("read model_update_dispatch.go: %v", err)
	}
	if !strings.Contains(string(src), "mcpRefreshResultMsg") {
		t.Fatal("mcpRefreshResultMsg must be registered in the update dispatcher")
	}
}
