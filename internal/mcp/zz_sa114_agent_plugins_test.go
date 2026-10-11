package mcp

// sa-114: Agent Plugins (agent-plugins.org 1.0.0) - discovery validation,
// mcp.json migration with ${PLUGIN_ROOT}/${PLUGIN_DATA} expansion, gate
// integration with per-source grant signatures.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func writePluginPackage(t *testing.T, dir, slug, name, schema string, withMCP bool, mcpBody string) {
	t.Helper()
	pluginDir := filepath.Join(dir, ".agent-plugins", slug)
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name": "` + name + `"`
	if schema != "" {
		manifest += `, "$schema": "` + schema + `"`
	}
	manifest += `}`
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	if withMCP {
		if err := os.WriteFile(filepath.Join(pluginDir, "mcp.json"), []byte(mcpBody), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDiscoverAgentPluginsValidation(t *testing.T) {
	dir := t.TempDir()
	writePluginPackage(t, dir, "good", "db-tools", "https://agent-plugins.org/schema/v1.json", false, "")
	writePluginPackage(t, dir, "no-name", "", "", false, "")
	writePluginPackage(t, dir, "foreign", "x", "https://example.com/other.json", false, "")
	// Directory without any manifest is not a plugin.
	if err := os.MkdirAll(filepath.Join(dir, ".agent-plugins", "bare"), 0755); err != nil {
		t.Fatal(err)
	}

	plugins := DiscoverAgentPlugins(dir)
	if len(plugins) != 1 {
		t.Fatalf("expected exactly the valid plugin, got %d: %+v", len(plugins), plugins)
	}
	if plugins[0].Name != "db-tools" {
		t.Fatalf("plugin name = %q, want db-tools", plugins[0].Name)
	}
}

func TestAgentPluginMCPServersLoadedAndGated(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate from the real user claude.json
	dir := t.TempDir()
	writePluginPackage(t, dir, "tools", "toolkit",
		"https://agent-plugins.org/schema/v1.json", true,
		`{"mcpServers": {"linter": {"type": "streamable-http", "url": "https://mcp.example/sse",
			"args": ["--root", "${PLUGIN_ROOT}"], "env": {"DATA": "${PLUGIN_DATA}"}}}}`)

	merged, warnings := MergeStartupServers(dir, nil)
	if len(merged) != 1 {
		t.Fatalf("expected 1 plugin server, got %d (warnings: %v)", len(merged), warnings)
	}
	srv := merged[0]
	if srv.Source != AgentPluginSource {
		t.Fatalf("source = %q, want %q", srv.Source, AgentPluginSource)
	}
	// streamable-http must map to the native http transport.
	if srv.Type != "http" {
		t.Fatalf("transport = %q, want http (streamable-http mapping)", srv.Type)
	}
	// ${PLUGIN_ROOT} expands to the plugin dir in args; ${PLUGIN_DATA} to
	// the per-plugin data dir in env. URL must NOT be rewritten.
	if len(srv.Args) != 2 || srv.Args[1] != filepath.Join(dir, ".agent-plugins", "tools") {
		t.Fatalf("args = %v, want PLUGIN_ROOT expanded", srv.Args)
	}
	if srv.Env["DATA"] == "" || srv.Env["DATA"] == "${PLUGIN_DATA}" {
		t.Fatalf("env DATA = %q, want PLUGIN_DATA expanded", srv.Env["DATA"])
	}
	if srv.URL != "https://mcp.example/sse" {
		t.Fatalf("url must not be rewritten, got %q", srv.URL)
	}

	// The plugin server is project-gated: blocked without a grant.
	_, blocked, _ := GateProjectServers(merged, dir)
	if len(blocked) != 1 {
		t.Fatalf("plugin server must be gated, blocked=%d", len(blocked))
	}

	// Approving records an agent-plugin-prefixed signature that then passes.
	grants, err := LoadProjectGrants(ProjectGrantsPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	grants.Approve("linter", projectGrantSignature(srv))
	if err := grants.Save(ProjectGrantsPath(dir)); err != nil {
		t.Fatal(err)
	}
	allowed, blocked2, _ := GateProjectServers(merged, dir)
	if len(blocked2) != 0 || len(allowed) != 1 {
		t.Fatalf("after approve: allowed=%d blocked=%d, want 1/0", len(allowed), len(blocked2))
	}

	// Grant isolation: a claude-project-style signature must NOT unlock the
	// plugin server (per-source grant isolation).
	grants2, _ := LoadProjectGrants(ProjectGrantsPath(dir))
	grants2.Grants["linter"] = "claude-project|" + serverSignature(srv)
	_ = grants2.Save(ProjectGrantsPath(dir))
	_, blocked3, _ := GateProjectServers(merged, dir)
	if len(blocked3) != 1 {
		t.Fatalf("cross-source grant must not unlock plugin server, blocked=%d", len(blocked3))
	}
}

func TestAgentPluginServerIsolatedFromProjectMCP(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate from the real user claude.json
	dir := t.TempDir()
	// Both a workspace .mcp.json and a plugin mcp.json defining the same
	// server: the workspace file (priority 3) wins; the plugin duplicate is
	// deduped with a warning, not duplicated.
	writePluginPackage(t, dir, "dup", "dup-plugin", "", true,
		`{"mcpServers": {"shared": {"type": "stdio", "command": "dup-cmd"}}}`)
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(
		`{"mcpServers": {"shared": {"type": "stdio", "command": "dup-cmd"}}}`), 0644); err != nil {
		t.Fatal(err)
	}
	merged, _ := MergeStartupServers(dir, nil)
	if len(merged) != 1 {
		t.Fatalf("duplicate must dedupe to 1, got %d", len(merged))
	}
	if merged[0].Source != "claude-project" {
		t.Fatalf("workspace .mcp.json must win, source=%q", merged[0].Source)
	}
}

func TestExpandPluginVarsScope(t *testing.T) {
	got := expandPluginVars("${PLUGIN_ROOT}/bin ${PLUGIN_DATA}/x ${OTHER}", "/r", "/d")
	if got != "/r/bin /d/x ${OTHER}" {
		t.Fatalf("expandPluginVars = %q", got)
	}
	_ = config.HomeDir // keep config import if assertions evolve
}
