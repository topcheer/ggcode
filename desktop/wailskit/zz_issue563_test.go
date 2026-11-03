//go:build goolm

package wailskit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue563RemoveDisconnectsFromSameSession(t *testing.T) {
	mcpTestHome(t)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "ggcode.yaml"), []byte("vendor: zai\n"), 0644); err != nil {
		t.Fatal(err)
	}
	setActiveChatBridge(t, ws)

	if err := AddMCPServer(map[string]string{
		"name":    "test-server",
		"type":    "stdio",
		"command": "echo",
		"args":    "hello",
	}); err != nil {
		t.Fatal(err)
	}

	wsMCP := filepath.Join(ws, "mcp_servers.yaml")
	if _, err := os.Stat(wsMCP); err != nil {
		t.Fatalf("workspace mcp_servers.yaml not created: %v", err)
	}

	if err := RemoveMCPServer("test-server"); err != nil {
		t.Fatalf("RemoveMCPServer failed: %v", err)
	}

	if data, err := os.ReadFile(wsMCP); err == nil && strings.Contains(string(data), "test-server") {
		t.Fatalf("test-server still present after remove:\n%s", data)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatalf("error reading mcp_servers.yaml: %v", err)
	}
}

func TestIssue563RemoveNonExistentServer(t *testing.T) {
	mcpTestHome(t)
	setActiveChatBridge(t, t.TempDir())

	err := RemoveMCPServer("non-existent-server")
	if err == nil {
		t.Fatal("expected error for non-existent server, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' error, got: %v", err)
	}
}
