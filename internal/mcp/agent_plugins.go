package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

const AgentPluginsDir = ".agent-plugins"

const AgentPluginSource = "agent-plugin"

type AgentPlugin struct {
	Name string

	Root string

	ManifestPath string
}

func (p AgentPlugin) SkillsDir() string {
	return filepath.Join(p.Root, "skills")
}

func (p AgentPlugin) MCPConfigPath() string {
	return filepath.Join(p.Root, "mcp.json")
}

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

			debug.Log("mcp", "agent plugin %s skipped ($schema %q is not agent-plugins.org)", entry.Name(), manifest.Schema)
			continue
		}
		out = append(out, AgentPlugin{Name: manifest.Name, Root: dir, ManifestPath: manifestPath})
	}
	return out
}

func expandPluginVars(s, pluginRoot, pluginData string) string {
	if pluginRoot != "" {
		s = strings.ReplaceAll(s, "${PLUGIN_ROOT}", pluginRoot)
	}
	if pluginData != "" {
		s = strings.ReplaceAll(s, "${PLUGIN_DATA}", pluginData)
	}
	return s
}
