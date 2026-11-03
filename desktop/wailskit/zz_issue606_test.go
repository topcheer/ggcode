//go:build goolm

package wailskit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeIssue606ClaudeFile(t *testing.T, dir string, servers map[string]any) {
	t.Helper()
	payload := map[string]any{
		"mcpServers": servers,
		"otherKey":   map[string]any{"untouched": true},
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func issue606ListHas(t *testing.T, name string) bool {
	t.Helper()
	servers, err := ListMCPServers()
	if err != nil {
		t.Fatalf("ListMCPServers: %v", err)
	}
	for _, s := range servers {
		if s.Name == name {
			return true
		}
	}
	return false
}

func TestIssue606MigratedServerVisibleAndRemovable(t *testing.T) {
	mcpTestHome(t)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "ggcode.yaml"), []byte("vendor: zai\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeIssue606ClaudeFile(t, ws, map[string]any{
		"migrated": map[string]any{
			"type":    "stdio",
			"command": "npx",
			"args":    []string{"-y", "migrated-mcp"},
		},
	})
	setActiveChatBridge(t, ws)

	if !issue606ListHas(t, "migrated") {
		t.Fatal("migrated .mcp.json server invisible in ListMCPServers")
	}

	if err := RemoveMCPServer("migrated"); err != nil {
		t.Fatalf("RemoveMCPServer(migrated): %v", err)
	}
	data, err := os.ReadFile(filepath.Join(ws, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("re-parse .mcp.json: %v\n%s", err, data)
	}
	servers, ok := parsed["mcpServers"].(map[string]any)
	if !ok || len(servers) != 0 {
		t.Fatalf(".mcp.json mcpServers not emptied: %s", data)
	}
	if _, ok := parsed["otherKey"]; !ok {
		t.Fatalf("unrelated top-level key dropped from .mcp.json: %s", data)
	}

	if issue606ListHas(t, "migrated") {
		t.Fatal("migrated server resurrected in ListMCPServers after removal")
	}
}

func TestIssue606RemoveDualSideNamePurgesBoth(t *testing.T) {
	mcpTestHome(t)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "ggcode.yaml"), []byte("vendor: zai\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setActiveChatBridge(t, ws)
	if err := AddMCPServer(map[string]string{
		"name":    "dual",
		"type":    "stdio",
		"command": "node",
		"args":    "dual-yaml.js",
	}); err != nil {
		t.Fatal(err)
	}
	writeIssue606ClaudeFile(t, ws, map[string]any{
		"dual": map[string]any{
			"type":    "stdio",
			"command": "node",
			"args":    []string{"dual-origin.js"},
		},
	})

	if !issue606ListHas(t, "dual") {
		t.Fatal("dual server missing from ListMCPServers before removal")
	}
	if err := RemoveMCPServer("dual"); err != nil {
		t.Fatalf("RemoveMCPServer(dual): %v", err)
	}

	wsMCP := filepath.Join(ws, "mcp_servers.yaml")
	if data, err := os.ReadFile(wsMCP); err == nil && strings.Contains(string(data), "dual") {
		t.Fatalf("dual still in mcp_servers.yaml:\n%s", data)
	}

	data, err := os.ReadFile(filepath.Join(ws, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "\"dual\"") {
		t.Fatalf("dual still in .mcp.json:\n%s", data)
	}

	if issue606ListHas(t, "dual") {
		t.Fatal("dual server resurrected in ListMCPServers after dual-side removal")
	}
}

func TestIssue606RemoveUnknownStillNotFound(t *testing.T) {
	mcpTestHome(t)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "ggcode.yaml"), []byte("vendor: zai\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setActiveChatBridge(t, ws)

	err := RemoveMCPServer("ghost")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}
