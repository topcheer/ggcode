//go:build goolm

package wailskit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// User report: "mcp面板上删除的mcp在瞬间就会被加回来，但是会显示没有配置"
// (deleting an MCP server in the panel instantly brings it back, shown as
// "not configured"). Reproduce against the USER-level Claude source
// (~/.claude.json), which the existing #606 tests do not cover (they only
// test the workspace .mcp.json).
func TestUserDeletionResurrectedFromUserClaudeSource(t *testing.T) {
	mcpTestHome(t) // isolated HOME
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "ggcode.yaml"), []byte("vendor: zai\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setActiveChatBridge(t, ws)

	// User-level Claude source defines the server (Claude Code writes its
	// user-scope MCP servers here).
	home, _ := os.UserHomeDir()
	userClaude := filepath.Join(home, ".claude.json")
	payload := map[string]any{
		"mcpServers": map[string]any{
			"pen": map[string]any{
				"type":    "stdio",
				"command": "npx",
				"args":    []string{"-y", "pen-mcp"},
			},
		},
		"otherState": map[string]any{"keep": true},
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	if err := os.WriteFile(userClaude, data, 0o644); err != nil {
		t.Fatal(err)
	}

	// Panel shows the migrated server.
	if !issue606ListHas(t, "pen") {
		t.Fatal("user-claude migrated server not listed before delete")
	}

	// User deletes it.
	if err := RemoveMCPServer("pen"); err != nil {
		t.Fatalf("RemoveMCPServer(pen): %v", err)
	}

	// Origin file must be cleaned.
	if data, err := os.ReadFile(userClaude); err == nil && strings.Contains(string(data), "pen") {
		t.Fatalf("pen still in ~/.claude.json after removal:\n%s", data)
	}

	// Anti-resurrect: the panel list must not bring it back.
	servers, err := ListMCPServers()
	if err != nil {
		t.Fatalf("ListMCPServers: %v", err)
	}
	for _, s := range servers {
		if s.Name == "pen" {
			t.Fatalf("deleted server resurrected in panel list with status=%q type=%q command=%q url=%q",
				s.Status, s.Type, s.Command, s.URL)
		}
	}
}

// Variant: server defined in workspace yaml (ggcode-native) AND the yaml
// holds OTHER servers too, so the file survives the delete. Guards the
// common multi-server case: after deleting one row, the remaining list must
// not re-merge the deleted name from any source, and the surviving servers
// must keep their config (not show up as "not configured").
func TestUserDeletionAmongMultipleServersKeepsOthersConfigured(t *testing.T) {
	mcpTestHome(t)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "ggcode.yaml"), []byte("vendor: zai\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setActiveChatBridge(t, ws)

	for _, name := range []string{"alpha", "beta"} {
		if err := AddMCPServer(map[string]string{
			"name":    name,
			"type":    "stdio",
			"command": "node",
			"args":    name + ".js",
		}); err != nil {
			t.Fatalf("AddMCPServer(%s): %v", name, err)
		}
	}

	if !issue606ListHas(t, "alpha") || !issue606ListHas(t, "beta") {
		t.Fatal("alpha/beta missing before delete")
	}

	if err := RemoveMCPServer("alpha"); err != nil {
		t.Fatalf("RemoveMCPServer(alpha): %v", err)
	}

	servers, err := ListMCPServers()
	if err != nil {
		t.Fatalf("ListMCPServers: %v", err)
	}
	for _, s := range servers {
		if s.Name == "alpha" {
			t.Fatalf("alpha resurrected: %+v", s)
		}
		if s.Name == "beta" && strings.TrimSpace(s.Command) == "" {
			t.Fatalf("beta lost its config after alpha deletion: %+v", s)
		}
	}
}
