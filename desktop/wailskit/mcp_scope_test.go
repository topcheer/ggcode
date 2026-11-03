//go:build goolm

package wailskit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func mcpTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func setActiveChatBridge(t *testing.T, workDir string) {
	t.Helper()
	globalMu.Lock()
	prev := activeChatBridge
	activeChatBridge = &ChatBridge{workingDir: workDir}
	globalMu.Unlock()
	t.Cleanup(func() {
		globalMu.Lock()
		activeChatBridge = prev
		globalMu.Unlock()
	})
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

func TestAddMCPServerWritesWorkspaceScope(t *testing.T) {
	home := mcpTestHome(t)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "ggcode.yaml"), []byte("vendor: zai\n"), 0644); err != nil {
		t.Fatal(err)
	}
	setActiveChatBridge(t, ws)

	if err := AddMCPServer(map[string]string{
		"name":    "ws-server",
		"type":    "stdio",
		"command": "npx",
		"args":    "-y ws-mcp",
	}); err != nil {
		t.Fatal(err)
	}

	wsMCP := filepath.Join(ws, "mcp_servers.yaml")
	if _, err := os.Stat(wsMCP); err != nil {
		t.Fatalf("workspace mcp_servers.yaml not written: %v", err)
	}
	if !strings.Contains(readFile(t, wsMCP), "ws-server") {
		t.Fatalf("ws-server missing from workspace mcp_servers.yaml:\n%s", readFile(t, wsMCP))
	}
	if _, err := os.Stat(filepath.Join(home, ".ggcode", "mcp_servers.yaml")); !os.IsNotExist(err) {
		t.Fatalf("global mcp_servers.yaml unexpectedly written: %v", err)
	}

	servers, err := ListMCPServers()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range servers {
		if s.Name == "ws-server" {
			found = true
		}
	}
	if !found {
		t.Fatalf("workspace server not listed: %+v", servers)
	}

	if err := RemoveMCPServer("ws-server"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(wsMCP); err == nil && strings.Contains(string(data), "ws-server") {
		t.Fatalf("ws-server still present after remove:\n%s", data)
	}
}

func TestAddMCPServerFallsBackToGlobalScope(t *testing.T) {
	home := mcpTestHome(t)
	globalMu.Lock()
	prev := activeChatBridge
	activeChatBridge = nil
	globalMu.Unlock()
	t.Cleanup(func() {
		globalMu.Lock()
		activeChatBridge = prev
		globalMu.Unlock()
	})

	if err := AddMCPServer(map[string]string{
		"name":    "global-server",
		"type":    "stdio",
		"command": "npx",
	}); err != nil {
		t.Fatal(err)
	}
	globalMCP := filepath.Join(home, ".ggcode", "mcp_servers.yaml")
	if !strings.Contains(readFile(t, globalMCP), "global-server") {
		t.Fatalf("global server missing from global mcp_servers.yaml:\n%s", readFile(t, globalMCP))
	}
}

func TestAddMCPServerPatchPreservesUnsetFields(t *testing.T) {
	mcpTestHome(t)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "ggcode.yaml"), []byte("vendor: zai\n"), 0644); err != nil {
		t.Fatal(err)
	}
	setActiveChatBridge(t, ws)

	if err := AddMCPServer(map[string]string{
		"name":            "http-server",
		"type":            "http",
		"url":             "https://old.example.com/mcp",
		"env_TOKEN":       "secret-token",
		"headers_X_Cache": "no",
	}); err != nil {
		t.Fatal(err)
	}

	if err := AddMCPServer(map[string]string{
		"name": "http-server",
		"url":  "https://new.example.com/mcp",
	}); err != nil {
		t.Fatal(err)
	}

	servers, err := ListMCPServers()
	if err != nil {
		t.Fatal(err)
	}
	var srv *MCPServerInfo
	for i := range servers {
		if servers[i].Name == "http-server" {
			srv = &servers[i]
		}
	}
	if srv == nil {
		t.Fatalf("http-server missing: %+v", servers)
	}
	if srv.URL != "https://new.example.com/mcp" {
		t.Errorf("URL not updated: %q", srv.URL)
	}
	if srv.Type != "http" {
		t.Errorf("type flipped to %q, want http", srv.Type)
	}
	if srv.Env["TOKEN"] != "secret-token" {
		t.Errorf("env TOKEN cleared: %+v", srv.Env)
	}
	if srv.Headers["X_Cache"] != "no" {
		t.Errorf("headers X_Cache cleared: %+v", srv.Headers)
	}
}

func TestAddCustomEndpointStoresKeyInKeysEnv(t *testing.T) {
	home := mcpTestHome(t)
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	SetConfig(cfg)
	t.Cleanup(func() { SetConfig(nil) })

	if err := AddCustomEndpoint("openai", "myep", "openai", "https://api.example.com/v1", "sk-plain-secret-123"); err != nil {
		t.Fatal(err)
	}

	keysEnv := readFile(t, filepath.Join(home, ".ggcode", "keys.env"))
	if !strings.Contains(keysEnv, "OPENAI_MYEP_API_KEY='sk-plain-secret-123'") {
		t.Fatalf("key not persisted to keys.env:\n%s", keysEnv)
	}

	vendorsYAML := readFile(t, filepath.Join(home, ".ggcode", "vendors.yaml"))
	if strings.Contains(vendorsYAML, "sk-plain-secret-123") {
		t.Fatalf("plaintext key leaked into vendors.yaml:\n%s", vendorsYAML)
	}
	if !strings.Contains(vendorsYAML, "${OPENAI_MYEP_API_KEY}") {
		t.Fatalf("endpoint key not stored as env reference:\n%s", vendorsYAML)
	}
}
