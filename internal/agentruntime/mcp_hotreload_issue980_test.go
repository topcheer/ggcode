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

func writeMCPDeleted(t *testing.T, configDir string, names []string) {
	t.Helper()
	if err := config.SaveMCPDeleted(configDir, names); err != nil {
		t.Fatal(err)
	}
}

func TestMCPHotReloadWatcherKeepsMigratedServersOnYAMLEdit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()

	if err := os.WriteFile(filepath.Join(ws, ".mcp.json"),
		[]byte(`{"mcpServers":{"migrated-srv":{"command":"echo","args":["hi"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	wsPath := filepath.Join(ws, "mcp_servers.yaml")
	writeMCPYAML(t, wsPath, "yaml-srv")

	startup := []config.MCPServerConfig{
		{Name: "yaml-srv", Command: "echo"},
		{Name: "migrated-srv", Command: "echo"},
	}
	mgr := plugin.NewMCPManager(startup, tool.NewRegistry(), "")
	w := NewMCPHotReload(globalDir, ws, mgr)

	ctx := context.Background()
	w.Start(ctx)
	defer func() {}()

	time.Sleep(20 * time.Millisecond)

	writeMCPYAML(t, wsPath, "another-yaml-srv")

	deadline := time.Now().Add(6 * time.Second)
	var gotYAML, gotAnother, gotMigrated bool
	for time.Now().Before(deadline) {
		for _, info := range mgr.Snapshot() {
			switch info.Name {
			case "yaml-srv":
				gotYAML = true
			case "another-yaml-srv":
				gotAnother = true
			case "migrated-srv":
				gotMigrated = true
			}
		}
		if gotYAML && gotAnother && gotMigrated {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("watcher reload lost migrated servers after yaml edit: yaml=%v another=%v migrated=%v", gotYAML, gotAnother, gotMigrated)
}

func TestMCPHotReloadWatcherTombstoneBlocksResurrectOnReload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()

	if err := os.WriteFile(filepath.Join(ws, ".mcp.json"),
		[]byte(`{"mcpServers":{"shared-srv":{"command":"echo","args":["hi"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	wsPath := filepath.Join(ws, "mcp_servers.yaml")
	writeMCPYAML(t, wsPath, "shared-srv")

	startup := []config.MCPServerConfig{{Name: "shared-srv", Command: "echo"}}
	mgr := plugin.NewMCPManager(startup, tool.NewRegistry(), "")
	w := NewMCPHotReload(globalDir, ws, mgr)

	for _, p := range w.watchedPaths() {
		w.seedState(p)
	}

	if err := os.WriteFile(wsPath, []byte("servers: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeMCPDeleted(t, globalDir, []string{"shared-srv"})

	w.checkAndReload(context.Background())

	for _, info := range mgr.Snapshot() {
		if info.Name == "shared-srv" {
			t.Fatal("tombstoned server resurrected by watcher reload merge")
		}
	}
}
