package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/tool"
)

// Regression: deleting an MCP server that also exists in a Claude migration
// source (.mcp.json) must NOT be resurrected by the hot-reload watcher.
//
// The watcher's checkAndReload used to run MergeStartupServers on every
// reload. mergeServers only dedupes Claude-source entries whose name is
// still present in the ggcode list; once the user deletes the entry, the
// same-name .mcp.json server is re-merged on the next poll - the delete
// never sticks and the process keeps running (user-visible: "MCP 卸载不掉").
//
// Fix: checkAndReload feeds the scope-resolved file list to Reload directly;
// the startup migration in interactive_core.go remains the single merge point.
func TestMCPHotReloadDeletedServerNotResurrectedFromClaudeSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()

	// Claude project source that still defines the server the user deleted.
	mcpJSON := filepath.Join(ws, ".mcp.json")
	if err := os.WriteFile(mcpJSON, []byte(`{"mcpServers":{"shared-srv":{"command":"echo","args":["hi"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// The persisted ggcode list still HAS the server at seed time.
	wsPath := filepath.Join(ws, "mcp_servers.yaml")
	writeMCPYAML(t, wsPath, "shared-srv")

	// Manager starts with the startup-merged set (both entries resolve to
	// the same name, so one plugin) - mirroring interactive_core.go.
	startup := []config.MCPServerConfig{{Name: "shared-srv", Command: "echo"}}
	mgr := plugin.NewMCPManager(startup, tool.NewRegistry())
	w := NewMCPHotReload(globalDir, ws, mgr)

	ctx := context.Background()
	w.Start(ctx)
	defer func() { /* goroutine exits with test process */ }()

	// User deletes the server from the persisted file.
	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(wsPath, []byte("servers: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		gone := true
		for _, info := range mgr.Snapshot() {
			if info.Name == "shared-srv" {
				gone = false
				break
			}
		}
		if gone {
			return // deletion reached the manager and stuck
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("deletion never reached the manager within 5s (watcher did not reload)")
}
