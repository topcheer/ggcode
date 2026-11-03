package wailskit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/plugin"
)

func TestSetMCPServerEnabledDisablePathPersistWins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}

	globalMu.Lock()
	prev := activeChatBridge

	activeChatBridge = &ChatBridge{mcpManager: plugin.NewMCPManager(nil, nil, "")}
	globalMu.Unlock()
	t.Cleanup(func() {
		globalMu.Lock()
		activeChatBridge = prev
		globalMu.Unlock()
	})

	if !SetMCPServerEnabled("ghost-server", false) {
		t.Fatal("disable path returned false with a successful persist (UI shows failed for a change that took effect)")
	}

	if !plugin.MCPDisabledIn("", "ghost-server") {
		t.Fatal("disable did not persist")
	}
}
