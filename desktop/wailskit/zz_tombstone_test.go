//go:build goolm

package wailskit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeletedServerNotResurrectedWhenExternalAppRewritesSource(t *testing.T) {
	mcpTestHome(t)
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

	writeClaude()
	if !issue606ListHas(t, "pen-app") {
		t.Fatal("pen-app should be visible before delete")
	}

	if err := RemoveMCPServer("pen-app"); err != nil {
		t.Fatalf("RemoveMCPServer(pen-app): %v", err)
	}

	writeClaude()

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
			return
		}
	}
	t.Fatal("manually re-added yaml server hidden by stale tombstone")
}
