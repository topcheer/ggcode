package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	Commands []PluginCommandConfig  `yaml:"commands"`
	Extra    map[string]interface{} `yaml:",inline"`
}

// PluginCommandConfig describes a single command tool within a plugin.
type PluginCommandConfig struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Execute     string   `yaml:"execute"`
	Args        []string `yaml:"args"`
}

// DefaultSystemPrompt is the built-in system prompt used when no custom system_prompt is set.
const DefaultSystemPrompt = `You are ggcode, an AI coding assistant running in a terminal.

## Identity
- You help users with coding tasks by reading, writing, editing files, and running commands
- You are precise, concise, and proactive
- You prefer small, focused changes over large rewrites
- You always verify your changes work

## Tool Usage Guidelines
### File Operations
- Read before edit — always understand existing code before modifying
- Use edit_file for targeted changes, write_file only for new files
- After editing, verify the change is correct

### Shell Commands
- Use run_command for builds, tests, git operations
- Prefer specific commands over generic ones
- Chain related commands with &&

### Search
- Use glob to find files, search_files for content
- Be specific with patterns to reduce noise

### Git
- Small, focused commits with clear messages
- Check status and diff before committing

## Behavior
- Ask for clarification when requirements are ambiguous
- Break complex tasks into steps
- Report progress during long operations
- When you find bugs, fix them with minimal changes
- Test your changes when possible
- Use @mentions to reference files for context

## Memory
- Use save_memory to persist useful patterns and decisions
- Check GGCODE.md for project context and conventions
- Learn from user preferences across sessions
`

// Config is the top-level configuration.
type Config struct {
	Provider      string                    `yaml:"provider"`
	Model         string                    `yaml:"model"`
	Language      string                    `yaml:"language"`
	SystemPrompt  string                    `yaml:"system_prompt"`
	Providers     map[string]ProviderConfig `yaml:"providers"`
	AllowedDirs   []string                  `yaml:"allowed_dirs"`
	MaxIterations int                       `yaml:"max_iterations"`
	ToolPerms     map[string]ToolPermission `yaml:"tool_permissions"`
	Plugins       []PluginConfigEntry       `yaml:"plugins"`
	MCPServers    []MCPServerConfig         `yaml:"mcp_servers"`
	Hooks         hooks.HookConfig          `yaml:"hooks"`
	DefaultMode   string                    `yaml:"default_mode"`
	SubAgents     SubAgentConfig            `yaml:"subagents"`
}

// SubAgentConfig holds sub-agent configuration.
type SubAgentConfig struct {
	MaxConcurrent int           `yaml:"max_concurrent"`
	Timeout       time.Duration `yaml:"timeout"`
	ShowOutput    bool          `yaml:"show_output"`
}

// DefaultConfig returns a config with sensible defaults.
func DefaultConfig() *Config {
	cfg := &Config{
		SystemPrompt:  DefaultSystemPrompt,
		Provider:      "anthropic",
		Model:         "claude-sonnet-4-20250514",
		Language:      "en",
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
	cfg.expandEnv()
	return cfg
}

// Load reads config from the given path. If the file doesn't exist, returns defaults.
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if err := cfg.Validate(); err != nil {
				return nil, err
			}
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
	cfg.expandEnv()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validating config %s: %w", path, err)
	}

	debug.Log("config", "Load: provider=%s model=%s max_iterations=%d", cfg.Provider, cfg.Model, cfg.MaxIterations)
	for name, pc := range cfg.Providers {
		hasKey := pc.APIKey != ""
		debug.Log("config", "  provider %s: base_url=%s api_key_set=%t max_tokens=%d", name, pc.BaseURL, hasKey, pc.MaxTokens)
	}

	return cfg, nil
}

func (c *Config) expandEnv() {
	c.Provider = ExpandEnv(c.Provider)
	c.Model = ExpandEnv(c.Model)
	c.SystemPrompt = ExpandEnv(c.SystemPrompt)
	c.DefaultMode = ExpandEnv(c.DefaultMode)
	for i, dir := range c.AllowedDirs {
		c.AllowedDirs[i] = ExpandEnv(dir)
	}
	for name, pc := range c.Providers {
		pc.APIKey = ExpandEnv(pc.APIKey)
		pc.BaseURL = ExpandEnv(pc.BaseURL)
		c.Providers[name] = pc
	}
	for i, plugin := range c.Plugins {
		plugin.Name = ExpandEnv(plugin.Name)
		plugin.Path = ExpandEnv(plugin.Path)
		plugin.Type = ExpandEnv(plugin.Type)
		for j, cmd := range plugin.Commands {
			cmd.Name = ExpandEnv(cmd.Name)
			cmd.Description = ExpandEnv(cmd.Description)
			cmd.Execute = ExpandEnv(cmd.Execute)
			for k, arg := range cmd.Args {
				cmd.Args[k] = ExpandEnv(arg)
			}
			plugin.Commands[j] = cmd
		}
		c.Plugins[i] = plugin
	}
	for i, mcp := range c.MCPServers {
		mcp.Name = ExpandEnv(mcp.Name)
		mcp.Command = ExpandEnv(mcp.Command)
		for j, arg := range mcp.Args {
			mcp.Args[j] = ExpandEnv(arg)
		}
		for key, val := range mcp.Env {
			mcp.Env[key] = ExpandEnv(val)
		}
		c.MCPServers[i] = mcp
	}
}

// Validate checks for invalid core configuration values that should fail fast.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.Provider) == "" {
		return fmt.Errorf("provider must not be empty")
	}
	if strings.TrimSpace(c.Model) == "" {
		return fmt.Errorf("model must not be empty")
	}
	if _, ok := c.Providers[c.Provider]; !ok {
		return fmt.Errorf("provider %q is not configured", c.Provider)
	}
	if c.MaxIterations <= 0 {
		return fmt.Errorf("max_iterations must be greater than 0")
	}
	if c.DefaultMode != "" {
		switch strings.ToLower(c.DefaultMode) {
		case "supervised", "plan", "auto", "bypass":
		default:
			return fmt.Errorf("default_mode %q must be one of supervised, plan, auto, bypass", c.DefaultMode)
		}
	}
	if c.SubAgents.MaxConcurrent < 0 {
		return fmt.Errorf("subagents.max_concurrent must not be negative")
	}
	if c.SubAgents.Timeout < 0 {
		return fmt.Errorf("subagents.timeout must not be negative")
	}
	for _, dir := range c.AllowedDirs {
		if strings.TrimSpace(dir) == "" {
			return fmt.Errorf("allowed_dirs must not contain empty entries")
		}
	}
	return nil
}

// GetProviderConfig returns the config for the active provider.
func (c *Config) GetProviderConfig() *ProviderConfig {
	if pc, ok := c.Providers[c.Provider]; ok {
		return &pc
	}
	return &ProviderConfig{}
}

// BuildSystemPrompt enhances the base system prompt with runtime context.
func BuildSystemPrompt(basePrompt, workingDir string, toolNames []string, gitStatus string, customCmds []string) string {
	if basePrompt == "" {
		basePrompt = DefaultSystemPrompt
	}

	var sb strings.Builder
	sb.WriteString(basePrompt)

	sb.WriteString("\n\n## Environment\n")
	sb.WriteString(fmt.Sprintf("- Working directory: %s\n", workingDir))
	sb.WriteString(fmt.Sprintf("- OS: %s/%s\n", runtime.GOOS, runtime.GOARCH))
	sb.WriteString(fmt.Sprintf("- Available tools: %s\n", strings.Join(toolNames, ", ")))

	if gitStatus != "" {
		sb.WriteString(fmt.Sprintf("- Git: %s\n", gitStatus))
	}

	if len(customCmds) > 0 {
		sb.WriteString(fmt.Sprintf("- Custom slash commands: %s\n", strings.Join(customCmds, ", ")))
	}

	return sb.String()
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
