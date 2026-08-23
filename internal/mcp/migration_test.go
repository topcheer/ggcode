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

// Tombstone semantics at the merge layer: tombstoned names are filtered from
// migration sources (the external-app resurrect path) but an explicit yaml
// entry with a tombstoned name is an intentional re-add and must survive.
func TestMergeStartupServersWithDeleted_TombstoneSemantics(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	claudeJSON := `{"mcpServers":{"pen-app":{"type":"stdio","command":"/Pen.app/mcp-server"},"other":{"type":"stdio","command":"node","args":["other.js"]}}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(claudeJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	explicit := []config.MCPServerConfig{{Name: "pen-app", Command: "node", Args: []string{"re-added.js"}}}
	merged, _ := MergeStartupServersWithDeleted(tmp, explicit, []string{"pen-app"})

	var penApp, other *config.MCPServerConfig
	for i := range merged {
		if merged[i].Name == "pen-app" {
			penApp = &merged[i]
		}
		if merged[i].Name == "other" {
			other = &merged[i]
		}
	}
	if penApp == nil {
		t.Fatal("explicit re-added pen-app must survive its tombstone")
	}
	if len(penApp.Args) != 1 || penApp.Args[0] != "re-added.js" {
		t.Fatalf("explicit entry must keep its own config, got %+v", penApp)
	}
	if penApp.Migrated {
		t.Fatal("explicit entry must not be replaced by the migrated source entry")
	}
	if other == nil {
		t.Fatal("non-tombstoned migration-source server must still merge in")
	}
}

// Without tombstones the same source entry merges (control for the test above).
func TestMergeStartupServers_NoDeleted_MigratesNormally(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	claudeJSON := `{"mcpServers":{"pen-app":{"type":"stdio","command":"/Pen.app/mcp-server"}}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(claudeJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	merged, _ := MergeStartupServers(tmp, nil)
	if len(merged) != 1 || merged[0].Name != "pen-app" || !merged[0].Migrated {
		t.Fatalf("expected pen-app to migrate without tombstone, got %+v", merged)
	}
}
