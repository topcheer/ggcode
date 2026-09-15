package wailskit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/plugin"
)

// TestSetMCPServerEnabledDisablePathPersistWins verifies #2389: when a
// manager is present but the name is not in its live plugin set (migration
// file, stale scope after a workspace switch), the disable-path return value
// must still reflect the PERSIST result - the same semantics the no-manager
// branch already has (#408: UI matches disk). The old form forwarded
// Disconnect()'s false: the UI reported "failed" for a disable that had
// already hit disk.
func TestSetMCPServerEnabledDisablePathPersistWins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	if err := os.MkdirAll(filepath.Join(home, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}

	globalMu.Lock()
	prev := activeChatBridge
	// Manager with zero live plugins: Disconnect("ghost") deterministically
	// returns false (the mcp_loader loop falls through) - exactly the
	// conditional window the issue describes.
	activeChatBridge = &ChatBridge{mcpManager: plugin.NewMCPManager(nil, nil)}
	globalMu.Unlock()
	t.Cleanup(func() {
		globalMu.Lock()
		activeChatBridge = prev
		globalMu.Unlock()
	})

	if !SetMCPServerEnabled("ghost-server", false) {
		t.Fatal("disable path returned false with a successful persist (UI shows failed for a change that took effect)")
	}
	// And the persist really happened.
	if !plugin.MCPDisabled("ghost-server") {
		t.Fatal("disable did not persist")
	}
}
