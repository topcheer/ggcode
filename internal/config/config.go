package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"

	"github.com/topcheer/ggcode/internal/hooks"
	"gopkg.in/yaml.v3"
)

// EndpointConfig describes a concrete vendor endpoint that maps to one protocol.
type EndpointConfig struct {
	DisplayName   string   `yaml:"display_name"`
	Protocol      string   `yaml:"protocol"`
	BaseURL       string   `yaml:"base_url"`
	APIKey        string   `yaml:"api_key,omitempty"`
	MaxTokens     int      `yaml:"max_tokens"`
	DefaultModel  string   `yaml:"default_model,omitempty"`
	SelectedModel string   `yaml:"selected_model,omitempty"`
	Models        []string `yaml:"models,omitempty"`
	Tags          []string `yaml:"tags,omitempty"`
}

// VendorConfig holds a real supplier plus its available endpoints.
type VendorConfig struct {
	DisplayName string                    `yaml:"display_name"`
	APIKey      string                    `yaml:"api_key,omitempty"`
	Endpoints   map[string]EndpointConfig `yaml:"endpoints"`
}

// ResolvedEndpoint is the runtime selection after config inheritance is applied.
type ResolvedEndpoint struct {
	VendorID     string
	VendorName   string
	EndpointID   string
	EndpointName string
	Protocol     string
	BaseURL      string
	APIKey       string
	Model        string
	MaxTokens    int
	Models       []string
	Tags         []string
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
	Vendor        string                    `yaml:"vendor"`
	Endpoint      string                    `yaml:"endpoint"`
	Model         string                    `yaml:"model"`
	Language      string                    `yaml:"language"`
	SystemPrompt  string                    `yaml:"system_prompt"`
	Vendors       map[string]VendorConfig   `yaml:"vendors"`
	AllowedDirs   []string                  `yaml:"allowed_dirs"`
	MaxIterations int                       `yaml:"max_iterations"`
	ToolPerms     map[string]ToolPermission `yaml:"tool_permissions"`
	Plugins       []PluginConfigEntry       `yaml:"plugins"`
	MCPServers    []MCPServerConfig         `yaml:"mcp_servers"`
	Hooks         hooks.HookConfig          `yaml:"hooks"`
	DefaultMode   string                    `yaml:"default_mode"`
	SubAgents     SubAgentConfig            `yaml:"subagents"`
	FilePath      string                    `yaml:"-"`
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
		Vendor:        "zai",
		Endpoint:      "cn-coding-openai",
		Model:         "glm-5-turbo",
		Language:      "en",
		AllowedDirs:   []string{"."},
		MaxIterations: 50,
		Vendors: map[string]VendorConfig{
			"zai": {
				DisplayName: "Z.ai",
				APIKey:      "${ZAI_API_KEY}",
				Endpoints: map[string]EndpointConfig{
					"cn-coding-openai": {
						DisplayName:   "CN Coding Plan",
						Protocol:      "openai",
						BaseURL:       "https://open.bigmodel.cn/api/coding/paas/v4",
						MaxTokens:     8192,
						DefaultModel:  "glm-5-turbo",
						SelectedModel: "glm-5-turbo",
						Models:        []string{"glm-5-turbo", "glm-5-plus"},
						Tags:          []string{"coding", "cn"},
					},
					"cn-coding-anthropic": {
						DisplayName: "CN Coding Plan (Anthropic)",
						Protocol:    "anthropic",
						BaseURL:     "https://open.bigmodel.cn/api/anthropic",
						MaxTokens:   8192,
						Tags:        []string{"coding", "cn", "anthropic"},
					},
					"global-coding-openai": {
						DisplayName:  "Global Coding Plan",
						Protocol:     "openai",
						MaxTokens:    8192,
						DefaultModel: "glm-5-turbo",
						Models:       []string{"glm-5-turbo", "glm-5-plus"},
						Tags:         []string{"coding", "global"},
					},
					"global-coding-anthropic": {
						DisplayName: "Global Coding Plan (Anthropic)",
						Protocol:    "anthropic",
						MaxTokens:   8192,
						Tags:        []string{"coding", "global", "anthropic"},
					},
					"cn-api-openai": {
						DisplayName:  "CN Standard API",
						Protocol:     "openai",
						MaxTokens:    8192,
						DefaultModel: "glm-4.5-air",
						Models:       []string{"glm-4.5-air", "glm-4.5"},
						Tags:         []string{"api", "cn"},
					},
					"global-api-openai": {
						DisplayName:  "Global Standard API",
						Protocol:     "openai",
						MaxTokens:    8192,
						DefaultModel: "glm-4.5-air",
						Models:       []string{"glm-4.5-air", "glm-4.5"},
						Tags:         []string{"api", "global"},
					},
				},
			},
		},
	}
	cfg.expandEnv()
	cfg.normalizeActiveModel()
	return cfg
}

// Load reads config from the given path. If the file doesn't exist, returns defaults.
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()
	cfg.FilePath = path

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg.normalizeActiveModel()
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
	if hasLegacyProviderKeys(raw) {
		return nil, fmt.Errorf("legacy provider/providers config is no longer supported; use vendor/endpoint/vendors instead")
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
	cfg.normalizeActiveModel()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validating config %s: %w", path, err)
	}

	debug.Log("config", "Load: vendor=%s endpoint=%s model=%s max_iterations=%d", cfg.Vendor, cfg.Endpoint, cfg.Model, cfg.MaxIterations)
	for vendorName, vc := range cfg.Vendors {
		debug.Log("config", "  vendor %s: api_key_set=%t endpoints=%d", vendorName, vc.APIKey != "", len(vc.Endpoints))
	}

	return cfg, nil
}

func (c *Config) expandEnv() {
	c.Vendor = ExpandEnv(c.Vendor)
	c.Endpoint = ExpandEnv(c.Endpoint)
	c.Model = ExpandEnv(c.Model)
	c.SystemPrompt = ExpandEnv(c.SystemPrompt)
	c.DefaultMode = ExpandEnv(c.DefaultMode)
	for i, dir := range c.AllowedDirs {
		c.AllowedDirs[i] = ExpandEnv(dir)
	}
	for vendorName, vc := range c.Vendors {
		vc.DisplayName = ExpandEnv(vc.DisplayName)
		vc.APIKey = ExpandEnv(vc.APIKey)
		for endpointName, ep := range vc.Endpoints {
			ep.DisplayName = ExpandEnv(ep.DisplayName)
			ep.Protocol = ExpandEnv(ep.Protocol)
			ep.BaseURL = ExpandEnv(ep.BaseURL)
			ep.APIKey = ExpandEnv(ep.APIKey)
			ep.DefaultModel = ExpandEnv(ep.DefaultModel)
			ep.SelectedModel = ExpandEnv(ep.SelectedModel)
			for i, model := range ep.Models {
				ep.Models[i] = ExpandEnv(model)
			}
			for i, tag := range ep.Tags {
				ep.Tags[i] = ExpandEnv(tag)
			}
			vc.Endpoints[endpointName] = ep
		}
		c.Vendors[vendorName] = vc
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
	if strings.TrimSpace(c.Vendor) == "" {
		return fmt.Errorf("vendor must not be empty")
	}
	if strings.TrimSpace(c.Endpoint) == "" {
		return fmt.Errorf("endpoint must not be empty")
	}
	vc, ok := c.Vendors[c.Vendor]
	if !ok {
		return fmt.Errorf("vendor %q is not configured", c.Vendor)
	}
	ep, ok := vc.Endpoints[c.Endpoint]
	if !ok {
		return fmt.Errorf("endpoint %q is not configured for vendor %q", c.Endpoint, c.Vendor)
	}
	if strings.TrimSpace(ep.Protocol) == "" {
		return fmt.Errorf("endpoint %q for vendor %q must declare a protocol", c.Endpoint, c.Vendor)
	}
	if strings.TrimSpace(c.Model) == "" && strings.TrimSpace(ep.SelectedModel) == "" && strings.TrimSpace(ep.DefaultModel) == "" {
		return fmt.Errorf("model must not be empty")
	}
	if c.MaxIterations <= 0 {
		return fmt.Errorf("max_iterations must be greater than 0")
	}
	if c.DefaultMode != "" {
		switch strings.ToLower(c.DefaultMode) {
		case "supervised", "plan", "auto", "bypass", "autopilot":
		default:
			return fmt.Errorf("default_mode %q must be one of supervised, plan, auto, bypass, autopilot", c.DefaultMode)
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

func (c *Config) normalizeActiveModel() {
	if c == nil || strings.TrimSpace(c.Model) != "" {
		return
	}
	if vc, ok := c.Vendors[c.Vendor]; ok {
		if ep, ok := vc.Endpoints[c.Endpoint]; ok {
			if ep.SelectedModel != "" {
				c.Model = ep.SelectedModel
			} else {
				c.Model = ep.DefaultModel
			}
		}
	}
}

func hasLegacyProviderKeys(raw map[string]interface{}) bool {
	if raw == nil {
		return false
	}
	_, hasProvider := raw["provider"]
	_, hasProviders := raw["providers"]
	return hasProvider || hasProviders
}

// ResolveActiveEndpoint resolves the selected vendor + endpoint into runtime settings.
func (c *Config) ResolveActiveEndpoint() (*ResolvedEndpoint, error) {
	if c == nil {
		return nil, fmt.Errorf("config is nil")
	}
	vc, ok := c.Vendors[c.Vendor]
	if !ok {
		return nil, fmt.Errorf("vendor %q is not configured", c.Vendor)
	}
	ep, ok := vc.Endpoints[c.Endpoint]
	if !ok {
		return nil, fmt.Errorf("endpoint %q is not configured for vendor %q", c.Endpoint, c.Vendor)
	}
	model := strings.TrimSpace(c.Model)
	if model == "" {
		model = strings.TrimSpace(ep.SelectedModel)
	}
	if model == "" {
		model = strings.TrimSpace(ep.DefaultModel)
	}
	if model == "" {
		return nil, fmt.Errorf("endpoint %q for vendor %q has no active model", c.Endpoint, c.Vendor)
	}
	apiKey := strings.TrimSpace(ep.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(vc.APIKey)
	}
	if strings.TrimSpace(ep.BaseURL) == "" {
		return nil, fmt.Errorf("endpoint %q for vendor %q has no base_url configured", c.Endpoint, c.Vendor)
	}
	maxTokens := ep.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8192
	}
	return &ResolvedEndpoint{
		VendorID:     c.Vendor,
		VendorName:   firstNonEmpty(vc.DisplayName, c.Vendor),
		EndpointID:   c.Endpoint,
		EndpointName: firstNonEmpty(ep.DisplayName, c.Endpoint),
		Protocol:     ep.Protocol,
		BaseURL:      ep.BaseURL,
		APIKey:       apiKey,
		Model:        model,
		MaxTokens:    maxTokens,
		Models:       append([]string(nil), ep.Models...),
		Tags:         append([]string(nil), ep.Tags...),
	}, nil
}

// VendorNames returns configured vendors in a stable order.
func (c *Config) VendorNames() []string {
	if c == nil {
		return nil
	}
	names := make([]string, 0, len(c.Vendors))
	for name := range c.Vendors {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// EndpointNames returns configured endpoints for the given vendor in a stable order.
func (c *Config) EndpointNames(vendor string) []string {
	if c == nil {
		return nil
	}
	vc, ok := c.Vendors[vendor]
	if !ok {
		return nil
	}
	names := make([]string, 0, len(vc.Endpoints))
	for name := range vc.Endpoints {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ActiveEndpointConfig returns a copy of the active endpoint config.
func (c *Config) ActiveEndpointConfig() *EndpointConfig {
	if c == nil {
		return nil
	}
	vc, ok := c.Vendors[c.Vendor]
	if !ok {
		return nil
	}
	ep, ok := vc.Endpoints[c.Endpoint]
	if !ok {
		return nil
	}
	return &ep
}

// SetActiveSelection updates the current vendor, endpoint, and model.
func (c *Config) SetActiveSelection(vendor, endpoint, model string) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	vc, ok := c.Vendors[vendor]
	if !ok {
		return fmt.Errorf("vendor %q is not configured", vendor)
	}
	ep, ok := vc.Endpoints[endpoint]
	if !ok {
		return fmt.Errorf("endpoint %q is not configured for vendor %q", endpoint, vendor)
	}
	if model == "" {
		model = firstNonEmpty(ep.SelectedModel, ep.DefaultModel)
	}
	if model == "" {
		return fmt.Errorf("endpoint %q for vendor %q has no model configured", endpoint, vendor)
	}
	ep.SelectedModel = model
	vc.Endpoints[endpoint] = ep
	c.Vendors[vendor] = vc
	c.Vendor = vendor
	c.Endpoint = endpoint
	c.Model = model
	return nil
}

// SetEndpointAPIKey updates the active endpoint or vendor-level API key.
func (c *Config) SetEndpointAPIKey(vendor, endpoint, apiKey string, vendorScoped bool) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	vc, ok := c.Vendors[vendor]
	if !ok {
		return fmt.Errorf("vendor %q is not configured", vendor)
	}
	if vendorScoped {
		vc.APIKey = strings.TrimSpace(apiKey)
		c.Vendors[vendor] = vc
		return nil
	}
	ep, ok := vc.Endpoints[endpoint]
	if !ok {
		return fmt.Errorf("endpoint %q is not configured for vendor %q", endpoint, vendor)
	}
	ep.APIKey = strings.TrimSpace(apiKey)
	vc.Endpoints[endpoint] = ep
	c.Vendors[vendor] = vc
	return nil
}

// Save persists the config to its configured file path.
func (c *Config) Save() error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	if strings.TrimSpace(c.FilePath) == "" {
		return fmt.Errorf("config file path is empty")
	}
	if err := c.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(c.FilePath), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	return os.WriteFile(c.FilePath, data, 0644)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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
