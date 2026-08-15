package wailskit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/tool"
)

// Test #498: AddMCPServer must propagate the new server list to the running
// session's MCP manager. The hot-reload watcher only polls the GLOBAL
// mcp_servers.yaml, so a workspace-scoped write is invisible to it — without
// the explicit Reload, a server added (or edited) for a workspace-bound
// session never takes effect, and even the UI Reconnect button cannot fix
// it (MCPPlugin.Connect short-circuits on the cached adapter).
func newTestBridgeWithMCP(t *testing.T, workingDir string) *ChatBridge {
	t.Helper()
	bridge := &ChatBridge{
		workingDir: workingDir,
		mcpManager: plugin.NewMCPManager(nil, tool.NewRegistry()),
	}
	return bridge
}

func TestAddMCPServerWorkspaceScopeReloadsManager(t *testing.T) {
	// Isolated global config (HOME) + a workspace with its own ggcode.yaml
	// so AddMCPServer writes the workspace mcp_servers.yaml (#248 scope).
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ggcode", "ggcode.yaml"), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "ggcode.yaml"), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	bridge := newTestBridgeWithMCP(t, ws)
	SetChatBridge(bridge)
	defer SetChatBridge(nil)

	// Precondition: manager knows no servers.
	if snap := bridge.mcpManager.Snapshot(); len(snap) != 0 {
		t.Fatalf("precondition: expected empty manager, got %d servers", len(snap))
	}

	if err := AddMCPServer(map[string]string{
		"name":    "echo-server",
		"type":    "stdio",
		"command": "echo",
	}); err != nil {
		t.Fatal(err)
	}

	// The manager must now know the server WITHOUT any file-watcher tick.
	// Reload marks new plugins pending/connecting — presence, not Connected
	// status, is the invariant (stdio connect actually spawns echo).
	var found bool
	for _, info := range bridge.mcpManager.Snapshot() {
		if info.Name == "echo-server" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("#498 regression: workspace-scoped AddMCPServer did not reach the running MCP manager (would stay dead until restart)")
	}

	// And it must be persisted in the workspace scope, not the global file.
	wsCfg, err := LoadConfigForWorkspace(ws)
	if err != nil {
		t.Fatal(err)
	}
	var persisted bool
	for _, s := range wsCfg.MCPServers {
		if s.Name == "echo-server" {
			persisted = true
		}
	}
	if !persisted {
		t.Fatal("server should be persisted in the workspace config")
	}
	globalCfg, err := config.Load(config.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range globalCfg.MCPServers {
		if s.Name == "echo-server" {
			t.Fatal("workspace-scoped add leaked into the global config")
		}
	}
}

// Editing an existing workspace server must REPLACE the manager's plugin
// (URL/command change), not just rewrite the yaml.
func TestAddMCPServerEditReplacesManagerPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ggcode", "ggcode.yaml"), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "ggcode.yaml"), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	bridge := newTestBridgeWithMCP(t, ws)
	SetChatBridge(bridge)
	defer SetChatBridge(nil)

	if err := AddMCPServer(map[string]string{
		"name":    "srv",
		"type":    "stdio",
		"command": "echo",
	}); err != nil {
		t.Fatal(err)
	}

	// Edit: change the command. The manager must swap the plugin for the new
	// config — the old code left the plugin's stale cfg in place.
	if err := AddMCPServer(map[string]string{
		"name":    "srv",
		"type":    "stdio",
		"command": "printf",
	}); err != nil {
		t.Fatal(err)
	}

	snap := bridge.mcpManager.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected exactly 1 server snapshot after edit, got %d", len(snap))
	}
	// Verify the persisted (and thus reloaded) command took the new value.
	wsCfg, err := LoadConfigForWorkspace(ws)
	if err != nil {
		t.Fatal(err)
	}
	var cmd string
	for _, s := range wsCfg.MCPServers {
		if s.Name == "srv" {
			cmd = s.Command
		}
	}
	if cmd != "printf" {
		t.Fatalf("edit did not persist: command=%q", cmd)
	}
}
