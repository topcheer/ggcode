package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/topcheer/ggcode/internal/debug"

	"github.com/topcheer/ggcode/internal/hooks"
	"gopkg.in/yaml.v3"
)

// ProviderConfig holds provider-specific settings.
type ProviderConfig struct {
	APIKey    string `yaml:"api_key"`
	BaseURL   string `yaml:"base_url"`
	MaxTokens int    `yaml:"max_tokens"`
}

// ToolPermission defines per-tool permission level in config.
type ToolPermission string

const (
	ToolPermAsk   ToolPermission = "ask"
	ToolPermAllow ToolPermission = "allow"
	ToolPermDeny  ToolPermission = "deny"
)

// MCPServerConfig defines an MCP server to connect to.
type MCPServerConfig struct {
	Name    string            `yaml:"name"`
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args"`
	Env     map[string]string `yaml:"env,omitempty"`
}

// PluginConfigEntry describes a single plugin from the config file.
type PluginConfigEntry struct {
	Name     string                 `yaml:"name"`
	Path     string                 `yaml:"path"`
	Type     string                 `yaml:"type"`
	Commands []PluginCommandConfig   `yaml:"commands"`
	Extra    map[string]interface{} `yaml:",inline"`
}

// PluginCommandConfig describes a single command tool within a plugin.
type PluginCommandConfig struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Execute     string   `yaml:"execute"`
	Args        []string `yaml:"args"`
}

// Config is the top-level configuration.
type Config struct {
	Provider      string                    `yaml:"provider"`
	Model         string                    `yaml:"model"`
	SystemPrompt  string                    `yaml:"system_prompt"`
	Providers     map[string]ProviderConfig `yaml:"providers"`
	AllowedDirs   []string                  `yaml:"allowed_dirs"`
	MaxIterations int                       `yaml:"max_iterations"`
	ToolPerms     map[string]ToolPermission `yaml:"tool_permissions"`
	Plugins    []PluginConfigEntry  `yaml:"plugins"`
	MCPServers []MCPServerConfig     `yaml:"mcp_servers"`
	Hooks      hooks.HookConfig      `yaml:"hooks"`
	DefaultMode string                    `yaml:"default_mode"`
	SubAgents   SubAgentConfig           `yaml:"subagents"`
}

// SubAgentConfig holds sub-agent configuration.
type SubAgentConfig struct {
	MaxConcurrent int           `yaml:"max_concurrent"`
	Timeout       time.Duration `yaml:"timeout"`
	ShowOutput    bool          `yaml:"show_output"`
}

// DefaultConfig returns a config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Provider:      "anthropic",
		Model:         "claude-sonnet-4-20250514",
		AllowedDirs:   []string{"."},
		MaxIterations: 50,
		Providers: map[string]ProviderConfig{
			"anthropic": {
				APIKey:    "${ANTHROPIC_API_KEY}",
				MaxTokens: 8192,
			},
			"openai": {
				APIKey:    "${OPENAI_API_KEY}",
				MaxTokens: 8192,
			},
			"gemini": {
				APIKey:    "${GEMINI_API_KEY}",
				MaxTokens: 8192,
			},
		},
	}
}

// Load reads config from the given path. If the file doesn't exist, returns defaults.
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	// Parse YAML into raw map for env expansion
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	// Expand env vars
	expanded := ExpandEnvRecursive(raw)

	// Re-marshal and unmarshal into struct
	expandedData, err := yaml.Marshal(expanded)
	if err != nil {
		return nil, fmt.Errorf("marshaling expanded config: %w", err)
	}

	if err := yaml.Unmarshal(expandedData, cfg); err != nil {
		return nil, fmt.Errorf("parsing expanded config: %w", err)
	}

	debug.Log("config", "Load: provider=%s model=%s max_iterations=%d", cfg.Provider, cfg.Model, cfg.MaxIterations)
	for name, pc := range cfg.Providers {
		key := pc.APIKey
		if len(key) > 10 {
			key = key[:10] + "..."
		}
		debug.Log("config", "  provider %s: base_url=%s api_key=%s max_tokens=%d", name, pc.BaseURL, key, pc.MaxTokens)
	}

	return cfg, nil
}

// GetProviderConfig returns the config for the active provider.
func (c *Config) GetProviderConfig() *ProviderConfig {
	if pc, ok := c.Providers[c.Provider]; ok {
		return &pc
	}
	return &ProviderConfig{}
}

// ExpandAllowedDirs resolves allowed_dirs entries relative to baseDir.
func (c *Config) ExpandAllowedDirs(baseDir string) []string {
	dirs := make([]string, 0, len(c.AllowedDirs))
	for _, d := range c.AllowedDirs {
		if d == "." {
			dirs = append(dirs, baseDir)
		} else if filepath.IsAbs(d) {
			dirs = append(dirs, d)
		} else {
			dirs = append(dirs, filepath.Join(baseDir, d))
		}
	}
	return dirs
}
