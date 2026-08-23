//go:build goolm

package wailskit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The core resurrect scenario: an external app (e.g. Pen.app) rewrites its
// registration into the Claude source file BEHIND OUR BACK after the user
// deleted the server in the ggcode panel. Without a tombstone the merge
// re-imports the name and the panel shows it again (status unknown, i.e.
// "没有配置").
func TestDeletedServerNotResurrectedWhenExternalAppRewritesSource(t *testing.T) {
	mcpTestHome(t) // isolated HOME
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "ggcode.yaml"), []byte("vendor: zai\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setActiveChatBridge(t, ws)

	home, _ := os.UserHomeDir()
	userClaude := filepath.Join(home, ".claude.json")

	penEntry := func() map[string]any {
		return map[string]any{
			"mcpServers": map[string]any{
				"pen-app": map[string]any{
					"type":    "stdio",
					"command": "/Applications/Pen.app/Contents/Resources/mcp-server",
					"args":    []string{"--agent", "claudeCodeCLI"},
				},
			},
		}
	}
	writeClaude := func() {
		data, _ := json.MarshalIndent(penEntry(), "", "  ")
		if err := os.WriteFile(userClaude, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// External app registered the server.
	writeClaude()
	if !issue606ListHas(t, "pen-app") {
		t.Fatal("pen-app should be visible before delete")
	}

	// User deletes it in the panel.
	if err := RemoveMCPServer("pen-app"); err != nil {
		t.Fatalf("RemoveMCPServer(pen-app): %v", err)
	}

	// The external app rewrites its registration right after (this is the
	// exact resurrect path the user reported).
	writeClaude()

	// The panel must NOT bring it back.
	servers, err := ListMCPServers()
	if err != nil {
		t.Fatalf("ListMCPServers: %v", err)
	}
	for _, s := range servers {
		if s.Name == "pen-app" {
			t.Fatalf("pen-app resurrected from external rewrite despite tombstone: %+v", s)
		}
	}
}

// Re-adding a tombstoned name revives it (tombstone cleared on Upsert).
func TestReaddingClearedTombstoneRevivesServer(t *testing.T) {
	mcpTestHome(t)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "ggcode.yaml"), []byte("vendor: zai\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setActiveChatBridge(t, ws)

	if err := AddMCPServer(map[string]string{
		"name":    "revive",
		"type":    "stdio",
		"command": "node",
		"args":    "revive.js",
	}); err != nil {
		t.Fatal(err)
	}
	if err := RemoveMCPServer("revive"); err != nil {
		t.Fatal(err)
	}
	if issue606ListHas(t, "revive") {
		t.Fatal("revive should be gone after delete")
	}

	// Re-add the same name: must come back and stay.
	if err := AddMCPServer(map[string]string{
		"name":    "revive",
		"type":    "stdio",
		"command": "node",
		"args":    "revive2.js",
	}); err != nil {
		t.Fatal(err)
	}
	if !issue606ListHas(t, "revive") {
		t.Fatal("re-added server should be visible (tombstone must clear on Upsert)")
	}
}

// Manually re-adding a tombstoned name to mcp_servers.yaml (bypassing the
// panel's UpsertMCPServer, so the tombstone file still lists the name) must
// still show the server: the explicit yaml list is an intentional re-add and
// wins over the tombstone. Tombstones only guard the migration-source side.
func TestManualYAMLReaddWinsOverTombstone(t *testing.T) {
	mcpTestHome(t)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "ggcode.yaml"), []byte("vendor: zai\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setActiveChatBridge(t, ws)

	if err := AddMCPServer(map[string]string{
		"name":    "manual",
		"type":    "stdio",
		"command": "node",
		"args":    "manual.js",
	}); err != nil {
		t.Fatal(err)
	}
	if err := RemoveMCPServer("manual"); err != nil {
		t.Fatal(err)
	}

	// User hand-edits mcp_servers.yaml to bring the name back, WITHOUT the
	// panel (so mcp_deleted.yaml still contains "manual").
	wsMCP := filepath.Join(ws, "mcp_servers.yaml")
	if err := os.WriteFile(wsMCP, []byte("- name: manual\n  type: stdio\n  command: node\n  args: [manual.js]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	servers, err := ListMCPServers()
	if err != nil {
		t.Fatalf("ListMCPServers: %v", err)
	}
	for _, s := range servers {
		if s.Name == "manual" && strings.TrimSpace(s.Command) != "" {
			return // visible with config: explicit re-add won
		}
	}
	t.Fatal("manually re-added yaml server hidden by stale tombstone")
}
