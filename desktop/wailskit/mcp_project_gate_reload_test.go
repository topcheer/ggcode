package wailskit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/mcp"
)

func TestReloadSessionMCPServersGatesProjectServers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, ".mcp.json"), []byte(`{
		"mcpServers": {
			"evil": {"type": "stdio", "command": "echo", "args": ["pwned"]}
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	bridge := newTestBridgeWithMCP(t, ws)
	var mu sync.Mutex
	var events []string
	var payloads []json.RawMessage
	bridge.OnStreamEvent = func(eventType string, data json.RawMessage) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, eventType)
		payloads = append(payloads, data)
	}

	cfg := &config.Config{MCPServers: []config.MCPServerConfig{
		{Name: "safe", Type: "stdio", Command: "echo"},
	}}
	reloadSessionMCPServers(bridge, cfg)

	for _, s := range bridge.mcpManager.Snapshot() {
		if s.Name == "evil" {
			t.Fatal("ungated project server reached the MCP manager via desktop reload")
		}
	}
	found := false
	for _, s := range bridge.mcpManager.Snapshot() {
		if s.Name == "safe" {
			found = true
		}
	}
	if !found {
		t.Fatal("user-approved/non-project server was dropped by the gate")
	}
	if len(events) != 1 || events[0] != "mcp:gate" {
		t.Fatalf("expected exactly one mcp:gate event, got %v", events)
	}
	if !strings.Contains(string(payloads[0]), `"evil"`) {
		t.Fatalf("mcp:gate payload must name the blocked server, got %s", payloads[0])
	}

	var servers []config.MCPServerConfig
	merged, _ := mcp.MergeStartupServersWithDeleted(ws, nil, nil)
	servers = merged
	if _, err := mcp.ApproveProjectServers(ws, servers, []string{"evil"}); err != nil {
		t.Fatalf("approving project server: %v", err)
	}
	reloadSessionMCPServers(bridge, &config.Config{})
	resurrected := false
	for _, s := range bridge.mcpManager.Snapshot() {
		if s.Name == "evil" {
			resurrected = true
		}
	}
	if !resurrected {
		t.Fatal("approved project server did not resurrect after reload")
	}
}
