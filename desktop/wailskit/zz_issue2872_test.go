package wailskit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/plugin"
)

// TestSetMCPServerEnabledEnablePathPersistWins verifies #2872: the enable
// path must mirror #2389's disable semantics - when a manager is present but
// the name is not in its live plugin set (definition joined after session
// start: workspace .mcp.json edit, migration file, stale scope after a
// workspace switch), Reconnect has no rebuild path and returned false, so
// the UI reported "enable failed" forever even though the persist had
// already hit disk. The fix reports the persist result and additionally
// rebuilds the session MCP set so the server actually connects in-session.
func TestSetMCPServerEnabledEnablePathPersistWins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	if err := os.MkdirAll(filepath.Join(home, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}

	globalMu.Lock()
	prev := activeChatBridge
	// Manager with zero live plugins: Reconnect("ghost") deterministically
	// returns false (the mcp_loader loop falls through) - exactly the
	// divergence window the issue describes.
	activeChatBridge = &ChatBridge{mcpManager: plugin.NewMCPManager(nil, nil, "")}
	globalMu.Unlock()
	t.Cleanup(func() {
		globalMu.Lock()
		activeChatBridge = prev
		globalMu.Unlock()
	})

	// Pre-existing disabled mark on disk, as left by a prior disable click.
	if err := plugin.SetMCPDisabledIn("", "ghost-server", true); err != nil {
		t.Fatalf("seed disabled state: %v", err)
	}

	if !SetMCPServerEnabled("ghost-server", true) {
		t.Fatal("enable path returned false with a successful persist (UI shows failed for a change that took effect)")
	}
	// And the enable really persisted.
	if plugin.MCPDisabledIn("", "ghost-server") {
		t.Fatal("enable did not persist (server still marked disabled)")
	}
}

// TestSetMCPServerEnabledEnablePathReloadIsHarmless asserts the rebuild path
// does not disturb the manager when the merged origin genuinely has nothing
// for the name (definition absent everywhere): the return value still
// reflects the persist (true), matching the no-manager branch's semantics
// for the same persist.
func TestSetMCPServerEnabledEnablePathReloadIsHarmless(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
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

	// Definition exists nowhere; enabling a name with no origin must still
	// report the persist result, not a live-set lookup failure.
	if !SetMCPServerEnabled("phantom-server", true) {
		t.Fatal("enable of an origin-less name must report the persist result, not Reconnect's false")
	}
	if plugin.MCPDisabledIn("", "phantom-server") {
		t.Fatal("enable did not persist")
	}
}
