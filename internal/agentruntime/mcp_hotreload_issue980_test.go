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

// writeMCPDeleted writes a deletion tombstone list to configDir/mcp_deleted.yaml.
func writeMCPDeleted(t *testing.T, configDir string, names []string) {
	t.Helper()
	if err := config.SaveMCPDeleted(configDir, names); err != nil {
		t.Fatal(err)
	}
}

// #980: the hot-reload watcher's checkAndReload must push the SAME merged set
// the runtime runs at startup (yaml ∪ .mcp.json, tombstone-filtered), not the
// yaml-only scope list. Before the fix, any panel write to mcp_servers.yaml
// re-kicked every Claude-migrated server from the running session within one
// poll (~2s) — the Add/Remove path was fixed in #979 but the watcher path was
// left pushing yaml-only.
func TestMCPHotReloadWatcherKeepsMigratedServersOnYAMLEdit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()

	// Claude project source providing a migrated server absent from yaml.
	if err := os.WriteFile(filepath.Join(ws, ".mcp.json"),
		[]byte(`{"mcpServers":{"migrated-srv":{"command":"echo","args":["hi"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// The workspace yaml holds an unrelated server.
	wsPath := filepath.Join(ws, "mcp_servers.yaml")
	writeMCPYAML(t, wsPath, "yaml-srv")

	// Manager starts with the startup-merged set — mirroring interactive_core.
	startup := []config.MCPServerConfig{
		{Name: "yaml-srv", Command: "echo"},
		{Name: "migrated-srv", Command: "echo"},
	}
	mgr := plugin.NewMCPManager(startup, tool.NewRegistry())
	w := NewMCPHotReload(globalDir, ws, mgr)

	ctx := context.Background()
	w.Start(ctx)
	defer func() { /* goroutine exits with test process */ }()

	time.Sleep(20 * time.Millisecond)
	// Panel-style edit: rewrite the yaml (content change, e.g. adding a server).
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
			return // watcher reloaded with the merged set: nothing kicked out
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("watcher reload lost migrated servers after yaml edit: yaml=%v another=%v migrated=%v", gotYAML, gotAnother, gotMigrated)
}

// #980 companion: a tombstoned name (deleted via panel, recorded in
// mcp_deleted.yaml) must NOT come back through the watcher's merge — even
// though the .mcp.json source still provides it. This is the property the old
// "no merge" comment was protecting; tombstones are the replacement.
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
	mgr := plugin.NewMCPManager(startup, tool.NewRegistry())
	w := NewMCPHotReload(globalDir, ws, mgr)

	// Drive the same code path the watcher goroutine would (deterministic,
	// no polling race): seed baselines FIRST (as Start does at watcher boot),
	// then apply the deletion and let checkAndReload observe the change.
	for _, p := range w.watchedPaths() {
		w.seedState(p)
	}

	// User deletes via the panel path: yaml entry gone + tombstone recorded.
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
