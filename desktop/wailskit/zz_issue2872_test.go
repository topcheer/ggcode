package wailskit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/plugin"
)

func TestSetMCPServerEnabledEnablePathPersistWins(t *testing.T) {
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

	if err := plugin.SetMCPDisabledIn("", "ghost-server", true); err != nil {
		t.Fatalf("seed disabled state: %v", err)
	}

	if !SetMCPServerEnabled("ghost-server", true) {
		t.Fatal("enable path returned false with a successful persist (UI shows failed for a change that took effect)")
	}

	if plugin.MCPDisabledIn("", "ghost-server") {
		t.Fatal("enable did not persist (server still marked disabled)")
	}
}

func TestSetMCPServerEnabledEnablePathReloadIsHarmless(t *testing.T) {
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

	if !SetMCPServerEnabled("phantom-server", true) {
		t.Fatal("enable of an origin-less name must report the persist result, not Reconnect's false")
	}
	if plugin.MCPDisabledIn("", "phantom-server") {
		t.Fatal("enable did not persist")
	}
}
