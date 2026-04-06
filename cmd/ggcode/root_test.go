package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestRootHelpUsesCompactLayout(t *testing.T) {
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Help(); err != nil {
		t.Fatalf("Help() error = %v", err)
	}

	help := out.String()
	want := []string{
		"Usage:\nggcode [flags]\nggcode [command]\n",
		"Available Commands:\n- completion: Generate shell completion script\n- mcp: Manage MCP server configuration\n",
		"Flags:\n- --allowedTools stringArray: tools to allow in pipe mode (can be repeated)\n",
		"- -h, --help: help for ggcode\n",
	}
	for _, snippet := range want {
		if !strings.Contains(help, snippet) {
			t.Fatalf("expected help to contain %q, got:\n%s", snippet, help)
		}
	}
	if strings.Contains(help, "\t") {
		t.Fatalf("expected help to avoid tab-based alignment, got:\n%s", help)
	}
}

func TestMCPInstallCommandPersistsServer(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/ggcode.yaml"

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--config", path, "mcp", "install", "stdio", "npx", "-y", "12306-mcp", "stdio"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(out.String(), "Installed MCP server 12306-mcp") {
		t.Fatalf("expected install output, got:\n%s", out.String())
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	found := false
	hasBogusConfig := false
	for _, server := range cfg.MCPServers {
		if server.Name == "12306-mcp" && server.Command == "npx" {
			found = true
		}
		if server.Name == "config" || server.Command == "--config" {
			hasBogusConfig = true
		}
	}
	if !found {
		t.Fatalf("expected installed 12306-mcp server in config, got %+v", cfg.MCPServers)
	}
	if hasBogusConfig {
		t.Fatalf("did not expect leaked --config MCP server, got %+v", cfg.MCPServers)
	}
}

func TestMCPInstallCommandPersistsServerEnv(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/ggcode.yaml"

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--config", path, "mcp", "install", "z-ai", "--env", "ZAI_AI_API_KEY=xxxx", "--", "npx", "-y", "@z_ai/mcp-server"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(out.String(), "Installed MCP server z-ai") {
		t.Fatalf("expected install output, got:\n%s", out.String())
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	found := false
	for _, server := range cfg.MCPServers {
		if server.Name == "z-ai" && server.Command == "npx" && server.Env["ZAI_AI_API_KEY"] == "xxxx" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected installed z-ai server with env in config, got %+v", cfg.MCPServers)
	}
}

func TestMCPListCommandShowsConfiguredServers(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/ggcode.yaml"

	cfg := config.DefaultConfig()
	cfg.FilePath = path
	cfg.MCPServers = []config.MCPServerConfig{
		{Name: "12306-mcp", Type: "stdio", Command: "npx", Args: []string{"-y", "12306-mcp", "stdio"}},
		{Name: "web-reader", Type: "http", URL: "https://mcp.example.com/api"},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--config", path, "mcp", "list"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := out.String()
	if !strings.Contains(output, "12306-mcp [stdio]\r\n  target: npx -y 12306-mcp stdio") {
		t.Fatalf("expected stdio MCP entry, got:\n%s", output)
	}
	if !strings.Contains(output, "web-reader [http]\r\n  target: https://mcp.example.com/api") {
		t.Fatalf("expected http MCP entry, got:\n%s", output)
	}
}

func TestMCPListCommandRepairsMalformedFlagEntry(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/ggcode.yaml"

	cfg := config.DefaultConfig()
	cfg.FilePath = path
	cfg.MCPServers = []config.MCPServerConfig{
		{Name: "config", Type: "stdio", Command: "--config", Args: []string{"/tmp/test.yaml", "stdio", "npx", "-y", "12306-mcp"}},
		{Name: "12306-mcp", Type: "stdio", Command: "npx", Args: []string{"-y", "12306-mcp"}},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--config", path, "mcp", "list"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := out.String()
	if strings.Contains(output, "config [stdio] --config") {
		t.Fatalf("expected malformed MCP entry to be hidden, got:\n%s", output)
	}
	if !strings.Contains(output, "12306-mcp [stdio]\r\n  target: npx -y 12306-mcp") {
		t.Fatalf("expected valid MCP entry, got:\n%s", output)
	}

	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, server := range reloaded.MCPServers {
		if server.Command == "--config" || server.Name == "config" {
			t.Fatalf("expected malformed MCP entry to be removed from config, got %+v", reloaded.MCPServers)
		}
	}
}

func TestMCPInstallCommandPersistsHTTPHeaders(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/ggcode.yaml"
	const name = "header-reader"

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--config", path, "mcp", "install", name, "-t", "http", "https://mcp.example.com/api", "--header", "Authorization: Bearer xxx"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(out.String(), "Installed MCP server "+name) {
		t.Fatalf("expected install output, got:\n%s", out.String())
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	found := false
	for _, server := range cfg.MCPServers {
		if server.Name == name && server.Type == "http" && server.URL == "https://mcp.example.com/api" && server.Headers["Authorization"] == "Bearer xxx" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected installed %s server with headers in config, got %+v", name, cfg.MCPServers)
	}
}

func TestMCPUninstallCommandRemovesServer(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/ggcode.yaml"

	cfg := config.DefaultConfig()
	cfg.FilePath = path
	cfg.MCPServers = []config.MCPServerConfig{{Name: "12306-mcp", Type: "stdio", Command: "npx", Args: []string{"-y", "12306-mcp", "stdio"}}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--config", path, "mcp", "uninstall", "12306-mcp"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(out.String(), "Uninstalled MCP server 12306-mcp") {
		t.Fatalf("expected uninstall output, got:\n%s", out.String())
	}

	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, server := range reloaded.MCPServers {
		if server.Name == "12306-mcp" {
			t.Fatalf("expected 12306-mcp to be removed, got %+v", reloaded.MCPServers)
		}
	}
}

func TestRootUsageUsesCompactFlagLayout(t *testing.T) {
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Usage(); err != nil {
		t.Fatalf("Usage() error = %v", err)
	}

	usage := out.String()
	if !strings.Contains(usage, "Flags:\n") ||
		!strings.Contains(usage, "- --config string: config file path\n") ||
		!strings.Contains(usage, "- -h, --help: help for ggcode\n") {
		t.Fatalf("expected compact flag layout in usage, got:\n%s", usage)
	}
}
