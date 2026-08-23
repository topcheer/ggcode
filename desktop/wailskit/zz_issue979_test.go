package wailskit

import (
	"os"
	"path/filepath"
	"testing"
)

// Tests for issue #979:
//  1. Add/Remove-triggered reload must push the merge-equivalent set
//     (yaml ∪ .mcp.json), not the yaml-only list — otherwise Claude-migrated
//     servers are silently kicked out of the running session (UI-visible but
//     tools gone) on any unrelated panel edit.
//  2. env/headers clearing must be constructible via the env_clear /
//     headers_clear form sentinels (empty non-nil map at the patch layer);
//     without any sentinel the old values must be preserved.

func setup979Workspace(t *testing.T) (home, ws string, bridge *ChatBridge) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ggcode", "ggcode.yaml"), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws = t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "ggcode.yaml"), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bridge = newTestBridgeWithMCP(t, ws)
	SetChatBridge(bridge)
	t.Cleanup(func() { SetChatBridge(nil) })
	return home, ws, bridge
}

func writeProjectMCPJSON(t *testing.T, ws string) {
	t.Helper()
	mcpJSON := `{
  "mcpServers": {
    "migrated-srv": {
      "type": "stdio",
      "command": "echo",
      "args": ["migrated"]
    }
  }
}`
	if err := os.WriteFile(filepath.Join(ws, ".mcp.json"), []byte(mcpJSON), 0o644); err != nil {
		t.Fatal(err)
	}
}

func snapshotHas(bridge *ChatBridge, name string) bool {
	for _, info := range bridge.mcpManager.Snapshot() {
		if info.Name == name {
			return true
		}
	}
	return false
}

// TestAddMCPServerReloadKeepsMigratedServers: with a workspace .mcp.json
// providing server X (absent from yaml), adding an unrelated yaml server Y
// must NOT disconnect X from the running session. Presence in the manager
// snapshot is the invariant (connect status depends on spawning).
func TestAddMCPServerReloadKeepsMigratedServers(t *testing.T) {
	_, ws, bridge := setup979Workspace(t)
	writeProjectMCPJSON(t, ws)

	if err := AddMCPServer(map[string]string{
		"name":    "yaml-srv",
		"type":    "stdio",
		"command": "echo",
	}); err != nil {
		t.Fatal(err)
	}

	if !snapshotHas(bridge, "yaml-srv") {
		t.Fatal("added yaml server missing from manager snapshot")
	}
	if !snapshotHas(bridge, "migrated-srv") {
		t.Fatal("#979 regression: reload with yaml-only set kicked .mcp.json server out of the running session")
	}
}

// TestRemoveMCPServerReloadKeepsOtherMigratedServers: removing a yaml server
// while .mcp.json provides a different server must leave the migrated server
// connected (and the removed one gone).
func TestRemoveMCPServerReloadKeepsOtherMigratedServers(t *testing.T) {
	_, ws, bridge := setup979Workspace(t)
	writeProjectMCPJSON(t, ws)

	if err := AddMCPServer(map[string]string{
		"name":    "yaml-srv",
		"type":    "stdio",
		"command": "echo",
	}); err != nil {
		t.Fatal(err)
	}
	if !snapshotHas(bridge, "migrated-srv") {
		t.Fatal("precondition: migrated server should be live before remove")
	}

	if err := RemoveMCPServer("yaml-srv"); err != nil {
		t.Fatal(err)
	}

	if snapshotHas(bridge, "yaml-srv") {
		t.Fatal("removed server still present in manager snapshot")
	}
	if !snapshotHas(bridge, "migrated-srv") {
		t.Fatal("#979 regression: removing an unrelated yaml server disconnected the .mcp.json server")
	}
}

// TestAddMCPServerRemoveMigratedServerFromOriginOnly: removing a server that
// lives ONLY in .mcp.json must delete it from the origin file and the session.
func TestAddMCPServerRemoveMigratedServerFromOriginOnly(t *testing.T) {
	_, ws, bridge := setup979Workspace(t)
	writeProjectMCPJSON(t, ws)

	// Force a reload so the migrated server is live first.
	if err := AddMCPServer(map[string]string{"name": "tmp", "type": "stdio", "command": "echo"}); err != nil {
		t.Fatal(err)
	}
	if !snapshotHas(bridge, "migrated-srv") {
		t.Fatal("precondition: migrated server should be live")
	}

	if err := RemoveMCPServer("migrated-srv"); err != nil {
		t.Fatal(err)
	}
	if snapshotHas(bridge, "migrated-srv") {
		t.Fatal("migrated server still in manager snapshot after origin removal")
	}
	// Origin file must no longer provide it (reload would resurrect it).
	data, err := os.ReadFile(filepath.Join(ws, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 0 && string(data[0]) != "{" {
		t.Fatalf("unexpected .mcp.json content: %q", data)
	}
	if contains := string(data); len(contains) > 0 && !jsonHasNoServer(t, contains, "migrated-srv") {
		t.Fatal("migrated server still present in .mcp.json after removal")
	}
}

// TestAddMCPServerExplicitClearEnvHeaders: editing a server with env_clear=1
// and headers_clear=1 must empty the stored env/headers (stale credentials
// must not resurrect), while an edit providing neither env_ keys nor the
// clear sentinel must preserve the old values.
func TestAddMCPServerExplicitClearEnvHeaders(t *testing.T) {
	_, ws, _ := setup979Workspace(t)

	// Seed with env + headers.
	if err := AddMCPServer(map[string]string{
		"name":         "srv",
		"type":         "http",
		"url":          "http://example.test",
		"env_TOKEN":    "secret-old",
		"headers_Auth": "Bearer old",
	}); err != nil {
		t.Fatal(err)
	}

	// Edit without env_/headers_ keys and WITHOUT clear sentinels: old
	// values must be preserved ("not provided" != "cleared").
	if err := AddMCPServer(map[string]string{
		"name": "srv",
		"type": "http",
		"url":  "http://v2.example.test",
	}); err != nil {
		t.Fatal(err)
	}
	srv := loadServerByName979(t, ws, "srv")
	if srv.Env["TOKEN"] != "secret-old" || srv.Headers["Auth"] != "Bearer old" {
		t.Fatalf("old env/headers not preserved on partial edit: env=%v headers=%v", srv.Env, srv.Headers)
	}

	// Edit with explicit clear sentinels: values must be gone.
	if err := AddMCPServer(map[string]string{
		"name":          "srv",
		"type":          "http",
		"url":           "http://v2.example.test",
		"env_clear":     "1",
		"headers_clear": "true",
	}); err != nil {
		t.Fatal(err)
	}
	srv = loadServerByName979(t, ws, "srv")
	if len(srv.Env) != 0 {
		t.Fatalf("#979 regression: env not cleared (stale values resurrected): %v", srv.Env)
	}
	if len(srv.Headers) != 0 {
		t.Fatalf("#979 regression: headers not cleared: %v", srv.Headers)
	}
}

func loadServerByName979(t *testing.T, ws, name string) (srv struct {
	Env     map[string]string
	Headers map[string]string
}) {
	t.Helper()
	cfg, err := LoadConfigForWorkspace(ws)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range cfg.MCPServers {
		if s.Name == name {
			return struct {
				Env     map[string]string
				Headers map[string]string
			}{s.Env, s.Headers}
		}
	}
	t.Fatalf("server %q not found in workspace config", name)
	return
}

func jsonHasNoServer(t *testing.T, content, name string) bool {
	t.Helper()
	// Minimal containment check: the quoted server key must be absent.
	return !containsSubstring(content, `"`+name+`"`)
}

func containsSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
