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

// Test #497: a global-file edit must NOT kick workspace-only servers out of a
// workspace session's manager, and a workspace-file edit must trigger reload.
//
// Old behavior: the watcher fed the GLOBAL file's list into Reload, so for a
// workspace-bound session (manager initially built from the WORKSPACE
// mcp_servers.yaml) any global-file write kicked every workspace-only server
// out as "removed" and injected global-only servers it never had.

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

// TestMCPHotReloadGlobalEditKeepsWorkspaceServers: editing the GLOBAL file
// while a workspace session runs must leave the workspace-only server's
// plugin alone.
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

	// Manager initially knows ONLY the workspace server (workspace scope —
	// mirrors BuildInteractiveRuntimeCore with LoadConfigForWorkspace cfg).
	mgr := plugin.NewMCPManager([]config.MCPServerConfig{
		{Name: "ws-only-srv", Type: "stdio", Command: "echo"},
	}, tool.NewRegistry(), "")

	w := NewMCPHotReload(globalDir, ws, mgr)
	// Force the initial baselines past both files' mtimes so the reload below
	// fires only for the deliberate post-construction edit. #521 replaced the
	// single lastMod watermark with per-path (mtime, hash) states.
	time.Sleep(10 * time.Millisecond)
	future := time.Now().Add(1 * time.Second)
	for _, p := range w.watchedPaths() {
		w.watched[p] = &watchState{exists: true, mtime: future, hash: hashFile(p)}
	}

	w.checkAndReload(context.Background()) // no change yet → no-op

	// Trigger: edit the GLOBAL file (add an unrelated server).
	writeMCPYAML(t, globalPath, "global-srv2")

	servers := w.resolveScopeMCPServers()
	var names []string
	for _, s := range servers {
		names = append(names, s.Name)
	}
	// Scope resolution must return the WORKSPACE list, not the global one.
	if len(names) != 1 || names[0] != "ws-only-srv" {
		t.Fatalf("#497 regression: scope-resolved list should be the workspace list [ws-only-srv], got %v", names)
	}
}

// TestMCPHotReloadGatesProjectMCPServers: a .mcp.json appearing behind a
// running session must not add child processes on reload until the user
// approves it - the same containment as startup, pinned at the watcher.
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
	// Seed baselines with a PAST mtime (mirrors Start()'s seeding of real
	// file states): detection advances on mtime, so a future watermark would
	// swallow every subsequent edit.
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

	// pollUntilReload waits for the watcher's reload to reach the manager.
	// Reload effects are asynchronous (existing #497 tests poll too), so a
	// reload is only known to have fired once its content is observable.
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

	// Phase 1: a real reload (distinct content so the watermark moves) that
	// merges the workspace .mcp.json - the gate must keep the project server
	// out while the user-owned server lands.
	writeMCPYAML(t, globalPath, "global-srv2")
	w.checkAndReload(context.Background())
	if !pollUntilReload("global-srv2", 5*time.Second) {
		t.Fatalf("reload never reached the manager; snapshot=%v", snapshotNames())
	}
	if snapshotNames()["proj-srv"] {
		t.Fatal("reload must not admit unapproved project .mcp.json server")
	}

	// Phase 2: approving via the same API the CLI uses must let the server
	// in on the NEXT reload (another watched edit re-fires the merge).
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

// TestMCPHotReloadWorkspaceFileTriggersReload: editing the WORKSPACE
// mcp_servers.yaml must be detected (previously only the global file was
// watched — a manual workspace edit never reloaded the manager).
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
	defer func() { /* goroutine exits with test process */ }()

	// Force a detection window: the workspace file mtime must be AFTER the
	// initial watermark for checkAndReload to see movement.
	time.Sleep(20 * time.Millisecond)
	writeMCPYAML(t, wsPath, "ws-renamed")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snap := mgr.Snapshot()
		for _, info := range snap {
			if info.Name == "ws-renamed" {
				return // reload reached the manager
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("#497: workspace mcp_servers.yaml edit never reached the manager (watcher still blind to workspace files)")
}

// TestMCPHotReloadNoReloadStormWithoutGlobalFile: when the global file does
// not exist (workspace-only setup), the shared watermark must still advance —
// otherwise every poll tick re-fires a reload.
func TestMCPHotReloadNoReloadStormWithoutGlobalFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, ".ggcode") // intentionally no file
	ws := t.TempDir()
	wsPath := filepath.Join(ws, "mcp_servers.yaml")
	writeMCPYAML(t, wsPath, "ws-only-srv")

	mgr := plugin.NewMCPManager(nil, tool.NewRegistry(), "")
	w := NewMCPHotReload(globalDir, ws, mgr)

	// Simulate one triggered reload followed by quiet ticks: after the first
	// checkAndReload, subsequent ticks must detect NO change (watermark
	// advanced past the workspace file even though the global file is absent).
	w.checkAndReload(context.Background()) // sees workspace mtime > zero watermark → reload + advance
	before := mgr.Snapshot()
	w.checkAndReload(context.Background()) // must be a no-op
	w.checkAndReload(context.Background())
	after := mgr.Snapshot()
	if len(before) != len(after) {
		t.Fatalf("reload storm: snapshot changed across quiet ticks (%d → %d)", len(before), len(after))
	}
}
