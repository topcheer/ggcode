package tui

import (
	"os"
	"strings"
	"testing"
)

func TestMCPPanelRefreshRunsOffUIGoroutine(t *testing.T) {
	src, err := os.ReadFile("mcp_panel.go")
	if err != nil {
		t.Fatalf("read mcp_panel.go: %v", err)
	}
	text := string(src)

	if !strings.Contains(text, "func (m *Model) mcpPanelRefresh(panel *mcpPanelState) tea.Cmd") {
		t.Fatal("mcpPanelRefresh must return tea.Cmd so the RPC runs off the UI goroutine")
	}

	if !strings.Contains(text, "found, outcome, count := mgr.Refresh(name)") {
		t.Fatal("Refresh must be invoked inside the async closure via the captured mgr")
	}

	if strings.Contains(text, "found, outcome, count := m.mcpManager.Refresh(name)") {
		t.Fatal("synchronous MCPManager.Refresh call in Update path regressed (TUI freeze)")
	}

	if !strings.Contains(text, "if cmd := m.mcpPanelRefresh(panel); cmd != nil") {
		t.Fatal("handleMCPPanelKey f-case must return the refresh tea.Cmd")
	}
}

func TestMCPPanelRefreshResultDispatched(t *testing.T) {
	src, err := os.ReadFile("model_update_dispatch.go")
	if err != nil {
		t.Fatalf("read model_update_dispatch.go: %v", err)
	}
	if !strings.Contains(string(src), "mcpRefreshResultMsg") {
		t.Fatal("mcpRefreshResultMsg must be registered in the update dispatcher")
	}
}
