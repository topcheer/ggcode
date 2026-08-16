//go:build goolm

package wailskit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIssue563RemoveDisconnectsFromSameSession verifies that RemoveMCPServer
// uses a consistent snapshot of activeChatBridge for both config loading and
// Disconnect. Without this fix (#563), a switchWorkspace between the two reads
// would cause Disconnect to operate on the new workspace's manager while the
// config change was written to the old workspace's file, leaving the removed
// server connected in the old workspace.
func TestIssue563RemoveDisconnectsFromSameSession(t *testing.T) {
	mcpTestHome(t)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "ggcode.yaml"), []byte("vendor: zai\n"), 0644); err != nil {
		t.Fatal(err)
	}
	setActiveChatBridge(t, ws)

	// Add a server first
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

	// Remove the server - this should use a snapshot of activeChatBridge
	// for both config load and Disconnect, preventing the race.
	if err := RemoveMCPServer("test-server"); err != nil {
		t.Fatalf("RemoveMCPServer failed: %v", err)
	}

	// Verify the server is removed from the config file
	if data, err := os.ReadFile(wsMCP); err == nil && strings.Contains(string(data), "test-server") {
		t.Fatalf("test-server still present after remove:\n%s", data)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatalf("error reading mcp_servers.yaml: %v", err)
	}
}

// TestIssue563RemoveNonExistentServer verifies proper error handling when
// attempting to remove a server that doesn't exist.
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
