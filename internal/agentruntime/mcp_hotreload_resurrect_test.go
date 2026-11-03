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

func TestMCPHotReloadDeletedServerNotResurrectedFromClaudeSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()

	mcpJSON := filepath.Join(ws, ".mcp.json")
	if err := os.WriteFile(mcpJSON, []byte(`{"mcpServers":{"shared-srv":{"command":"echo","args":["hi"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	wsPath := filepath.Join(ws, "mcp_servers.yaml")
	writeMCPYAML(t, wsPath, "shared-srv")

	startup := []config.MCPServerConfig{{Name: "shared-srv", Command: "echo"}}
	mgr := plugin.NewMCPManager(startup, tool.NewRegistry(), "")
	w := NewMCPHotReload(globalDir, ws, mgr)

	ctx := context.Background()
	w.Start(ctx)
	defer func() {}()

	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(wsPath, []byte("servers: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeMCPDeleted(t, globalDir, []string{"shared-srv"})

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
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("deletion never reached the manager within 5s (watcher did not reload)")
}
