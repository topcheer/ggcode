package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestMergeStartupServers_DedupsClaudeSources(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	project := filepath.Join(tmp, "project")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	globalConfig := `{"mcpServers":{"same-cmd":{"type":"stdio","command":"npx","args":["-y","pkg"]},"remote":{"type":"http","url":"https://example.com/mcp"}}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(globalConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	projectConfig := `{"mcpServers":{"project-only":{"type":"stdio","command":"node","args":["server.js"]}}}`
	if err := os.WriteFile(filepath.Join(project, ".mcp.json"), []byte(projectConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	explicit := []config.MCPServerConfig{
		{Name: "local-explicit", Command: "bin/server"},
		{Name: "explicit-dup", Command: "npx", Args: []string{"-y", "pkg"}},
	}

	merged, warnings := MergeStartupServers(project, explicit)
	if len(merged) != 4 {
		t.Fatalf("expected 4 merged servers, got %d", len(merged))
	}
	if len(warnings) == 0 {
		t.Fatal("expected dedup warning for duplicate Claude server")
	}
	foundProject := false
	foundRemoteMigrated := false
	for _, server := range merged {
		switch server.Name {
		case "project-only":
			foundProject = server.Migrated && server.Source == "claude-project"
		case "remote":
			foundRemoteMigrated = server.Migrated && server.Source == "claude-user"
		case "same-cmd":
			t.Fatal("duplicate Claude stdio server should have been suppressed")
		}
	}
	if !foundProject {
		t.Fatal("expected project .mcp.json server to be migrated")
	}
	if !foundRemoteMigrated {
		t.Fatal("expected global Claude HTTP server to be migrated")
	}
}

func TestPersistUserClaudeServers_WritesIntoConfig(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	globalConfig := `{"mcpServers":{"web-reader":{"type":"http","url":"https://example.com/mcp"}}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(globalConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(tmp, "ggcode.yaml")
	cfg := config.DefaultConfig()
	cfg.FilePath = cfgPath

	warnings, persisted, err := PersistUserClaudeServers(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !persisted {
		t.Fatal("expected Claude MCP servers to be persisted")
	}
	if len(warnings) == 0 {
		t.Fatal("expected info warning about persisted migration")
	}
	saved, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.MCPServers) != 1 || saved.MCPServers[0].Name != "web-reader" {
		t.Fatalf("unexpected persisted MCP servers: %+v", saved.MCPServers)
	}
}

func TestPersistUserClaudeServers_SkipsProjectMCPFile(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	project := filepath.Join(tmp, "project")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	projectConfig := `{"mcpServers":{"project-only":{"type":"stdio","command":"node","args":["server.js"]}}}`
	if err := os.WriteFile(filepath.Join(project, ".mcp.json"), []byte(projectConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(tmp, "ggcode.yaml")
	cfg := config.DefaultConfig()
	cfg.FilePath = cfgPath

	persistedWarnings, persisted, err := PersistUserClaudeServers(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if persisted {
		t.Fatalf("did not expect project .mcp.json to be persisted: warnings=%v", persistedWarnings)
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatalf("expected no config file to be written, stat err=%v", err)
	}
}
