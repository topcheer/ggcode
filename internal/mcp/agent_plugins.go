package mcp

// Agent Plugins discovery (agent-plugins.org open standard 1.0.0, published
// 2026-08-06 by OpenAI + AWS + Cursor + GitHub + VS Code + Vercel; research
// sa-114). A plugin is a directory under <workspace>/.agent-plugins/ that
// ships a plugin.json manifest plus optional fixed-location components:
// skills/<name>/SKILL.md (agentskills.io format) and mcp.json (MCP server
// config, Claude mcpServers shape). The spec makes the manifest REQUIRED:
// a directory without a valid plugin.json is not a plugin at all, so it is
// skipped whole (invalid entry => invalid package, never half-loaded).
//
// Client-side duties per spec: load manifest, validate name, discover
// components at fixed locations, and isolate per-component failures so one
// bad mcp.json entry never blocks sibling skills from loading.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

// AgentPluginsDir is the fixed workspace location plugins install to.
const AgentPluginsDir = ".agent-plugins"

// AgentPluginSource is the MCP config Source marker for servers loaded from
// a plugin's mcp.json. Gated by the project MCP gate exactly like
// claude-project: plugin content arrives with cloned repositories.
const AgentPluginSource = "agent-plugin"

// AgentPlugin is one validated plugin package.
type AgentPlugin struct {
	// Name is the manifest name (validated non-empty).
	Name string
	// Root is the absolute plugin directory (component base, ${PLUGIN_ROOT}).
	Root string
	// ManifestPath is the plugin.json path that validated the package.
	ManifestPath string
}

// SkillsDir returns the fixed skills component location, if present.
func (p AgentPlugin) SkillsDir() string {
	return filepath.Join(p.Root, "skills")
}

// MCPConfigPath returns the fixed mcp.json component location.
func (p AgentPlugin) MCPConfigPath() string {
	return filepath.Join(p.Root, "mcp.json")
}

// DataDir returns the per-plugin writable data directory,
// ${PLUGIN_DATA}: ~/.ggcode/plugin-data/<name>/. Created lazily by the
// servers that use it, not here.
func (p AgentPlugin) DataDir() string {
	home := config.HomeDir()
	if strings.TrimSpace(home) == "" || strings.TrimSpace(p.Name) == "" {
		return ""
	}
	return filepath.Join(home, ".ggcode", "plugin-data", p.Name)
}

type agentPluginManifest struct {
	Name   string `json:"name"`
	Schema string `json:"$schema"`
}

// DiscoverAgentPlugins scans <workingDir>/.agent-plugins/*/ for valid
// plugin packages. Every skip (unreadable/invalid manifest, non-standard
// $schema) is logged via debug.Log so broken packages are visible in
// diagnostics but never abort sibling plugins - the spec's per-package
// failure isolation.
func DiscoverAgentPlugins(workingDir string) []AgentPlugin {
	if strings.TrimSpace(workingDir) == "" {
		return nil
	}
	root := filepath.Join(workingDir, AgentPluginsDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []AgentPlugin
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		manifestPath := filepath.Join(dir, "plugin.json")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			// No/unreadable manifest: not a plugin package (manifest is
			// required by spec). Skills-only dirs are NOT special-cased.
			debug.Log("mcp", "agent plugin %s skipped (no readable plugin.json): %v", entry.Name(), err)
			continue
		}
		var manifest agentPluginManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			debug.Log("mcp", "agent plugin %s skipped (invalid plugin.json): %v", entry.Name(), err)
			continue
		}
		if strings.TrimSpace(manifest.Name) == "" {
			debug.Log("mcp", "agent plugin %s skipped (manifest name is empty)", entry.Name())
			continue
		}
		if manifest.Schema != "" && !strings.Contains(manifest.Schema, "agent-plugins") {
			// A $schema pointing at a different standard is an explicit
			// opt-out, not an error: ignore the package entirely.
			debug.Log("mcp", "agent plugin %s skipped ($schema %q is not agent-plugins.org)", entry.Name(), manifest.Schema)
			continue
		}
		out = append(out, AgentPlugin{Name: manifest.Name, Root: dir, ManifestPath: manifestPath})
	}
	return out
}

// expandPluginVars substitutes ${PLUGIN_ROOT} and ${PLUGIN_DATA} in the
// fields the spec allows (Args and Env values ONLY - never command, url, or
// headers: those must not carry plugin-filesystem state).
func expandPluginVars(s, pluginRoot, pluginData string) string {
	if pluginRoot != "" {
		s = strings.ReplaceAll(s, "${PLUGIN_ROOT}", pluginRoot)
	}
	if pluginData != "" {
		s = strings.ReplaceAll(s, "${PLUGIN_DATA}", pluginData)
	}
	return s
}
