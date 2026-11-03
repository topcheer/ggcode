package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/tool"
)

func writeMCPYAML(t *testing.T, path, serverName string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	yml := "- name: " + serverName + "\n  type: stdio\n  command: echo\n"
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMCPHotReloadGlobalEditKeepsWorkspaceServers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	globalPath := filepath.Join(globalDir, "mcp_servers.yaml")
	writeMCPYAML(t, globalPath, "global-srv")

	ws := t.TempDir()
	writeMCPYAML(t, filepath.Join(ws, "mcp_servers.yaml"), "ws-only-srv")

	mgr := plugin.NewMCPManager([]config.MCPServerConfig{
		{Name: "ws-only-srv", Type: "stdio", Command: "echo"},
	}, tool.NewRegistry(), "")

	w := NewMCPHotReload(globalDir, ws, mgr)

	time.Sleep(10 * time.Millisecond)
	future := time.Now().Add(1 * time.Second)
	for _, p := range w.watchedPaths() {
		w.watched[p] = &watchState{exists: true, mtime: future, hash: hashFile(p)}
	}

	w.checkAndReload(context.Background())

	writeMCPYAML(t, globalPath, "global-srv2")

	servers := w.resolveScopeMCPServers()
	var names []string
	for _, s := range servers {
		names = append(names, s.Name)
	}

	if len(names) != 1 || names[0] != "ws-only-srv" {
		t.Fatalf("#497 regression: scope-resolved list should be the workspace list [ws-only-srv], got %v", names)
	}
}

func TestMCPHotReloadGatesProjectMCPServers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(mcp.ProjectGateEnvVar, "")
	globalDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	globalPath := filepath.Join(globalDir, "mcp_servers.yaml")
	writeMCPYAML(t, globalPath, "global-srv")

	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, ".mcp.json"), []byte(`{"mcpServers":{"proj-srv":{"type":"stdio","command":"node","args":["server.js"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := plugin.NewMCPManager([]config.MCPServerConfig{
		{Name: "global-srv", Type: "stdio", Command: "echo"},
	}, tool.NewRegistry(), "")
	w := NewMCPHotReload(globalDir, ws, mgr)
	time.Sleep(10 * time.Millisecond)

	past := time.Now().Add(-1 * time.Second)
	for _, p := range w.watchedPaths() {
		w.watched[p] = &watchState{exists: true, mtime: past, hash: hashFile(p)}
	}

	snapshotNames := func() map[string]bool {
		out := map[string]bool{}
		for _, s := range mgr.Snapshot() {
			out[s.Name] = true
		}
		return out
	}

	pollUntilReload := func(want string, deadline time.Duration) bool {
		timeout := time.Now().Add(deadline)
		for time.Now().Before(timeout) {
			if snapshotNames()[want] {
				return true
			}
			time.Sleep(20 * time.Millisecond)
		}
		return false
	}

	writeMCPYAML(t, globalPath, "global-srv2")
	w.checkAndReload(context.Background())
	if !pollUntilReload("global-srv2", 5*time.Second) {
		t.Fatalf("reload never reached the manager; snapshot=%v", snapshotNames())
	}
	if snapshotNames()["proj-srv"] {
		t.Fatal("reload must not admit unapproved project .mcp.json server")
	}

	merged, _ := mcp.MergeStartupServersWithDeleted(ws, nil, nil)
	if approved, err := mcp.ApproveProjectServers(ws, merged, []string{"proj-srv"}); err != nil || approved != 1 {
		t.Fatalf("approve: %d %v", approved, err)
	}
	writeMCPYAML(t, globalPath, "global-srv3")
	w.checkAndReload(context.Background())
	if !pollUntilReload("proj-srv", 5*time.Second) {
		t.Fatalf("approved project server must appear after approval reload; snapshot=%v", snapshotNames())
	}
}

func TestMCPHotReloadWorkspaceFileTriggersReload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	wsPath := filepath.Join(ws, "mcp_servers.yaml")
	writeMCPYAML(t, wsPath, "ws-only-srv")

	mgr := plugin.NewMCPManager(nil, tool.NewRegistry(), "")
	w := NewMCPHotReload(globalDir, ws, mgr)
	w.Start(context.Background())
	defer func() {}()

	time.Sleep(20 * time.Millisecond)
	writeMCPYAML(t, wsPath, "ws-renamed")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snap := mgr.Snapshot()
		for _, info := range snap {
			if info.Name == "ws-renamed" {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("#497: workspace mcp_servers.yaml edit never reached the manager (watcher still blind to workspace files)")
}

func TestMCPHotReloadNoReloadStormWithoutGlobalFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, ".ggcode")
	ws := t.TempDir()
	wsPath := filepath.Join(ws, "mcp_servers.yaml")
	writeMCPYAML(t, wsPath, "ws-only-srv")

	mgr := plugin.NewMCPManager(nil, tool.NewRegistry(), "")
	w := NewMCPHotReload(globalDir, ws, mgr)

	w.checkAndReload(context.Background())
	before := mgr.Snapshot()
	w.checkAndReload(context.Background())
	w.checkAndReload(context.Background())
	after := mgr.Snapshot()
	if len(before) != len(after) {
		t.Fatalf("reload storm: snapshot changed across quiet ticks (%d → %d)", len(before), len(after))
	}
}

func TestMCPHotReloadWatchesProjectGrants(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	w := NewMCPHotReload(globalDir, ws, plugin.NewMCPManager(nil, tool.NewRegistry(), ""))

	grantsPath := mcp.ProjectGrantsPath(ws)
	if grantsPath == "" {
		t.Fatal("precondition: grants path must be resolvable for a workspace")
	}
	found := false
	for _, p := range w.watchedPaths() {
		if p == grantsPath {
			found = true
		}
	}
	if !found {
		t.Fatalf("grants file %s missing from watchedPaths: %v", grantsPath, w.watchedPaths())
	}
}
