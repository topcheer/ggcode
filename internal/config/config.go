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
	ContextWindow int      `yaml:"context_window,omitempty"`
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
	VendorID      string
	VendorName    string
	EndpointID    string
	EndpointName  string
	Protocol      string
	BaseURL       string
	APIKey        string
	Model         string
	ContextWindow int
	MaxTokens     int
	Models        []string
	Tags          []string
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
	Name       string            `yaml:"name"`
	Type       string            `yaml:"type,omitempty"`
	Command    string            `yaml:"command,omitempty"`
	Args       []string          `yaml:"args,omitempty"`
	Env        map[string]string `yaml:"env,omitempty"`
	URL        string            `yaml:"url,omitempty"`
	Headers    map[string]string `yaml:"headers,omitempty"`
	Source     string            `yaml:"-"`
	OriginPath string            `yaml:"-"`
	Migrated   bool              `yaml:"-"`
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
- Check project memory files like GGCODE.md, AGENTS.md, and CLAUDE.md for project context and conventions
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
	FirstRun      bool                      `yaml:"-"`
}

// SubAgentConfig holds sub-agent configuration.
type SubAgentConfig struct {
	MaxConcurrent int           `yaml:"max_concurrent"`
	Timeout       time.Duration `yaml:"timeout"`
	ShowOutput    bool          `yaml:"show_output"`
}

func defaultEndpoint(displayName, protocol, baseURL, defaultModel string, models []string, tags ...string) EndpointConfig {
	ep := EndpointConfig{
		DisplayName:   displayName,
		Protocol:      protocol,
		BaseURL:       baseURL,
		ContextWindow: inferContextWindow(defaultModel, protocol),
		MaxTokens:     inferMaxOutputTokens(defaultModel, protocol),
		DefaultModel:  defaultModel,
		Models:        append([]string(nil), models...),
		Tags:          append([]string(nil), tags...),
	}
	if defaultModel != "" {
		ep.SelectedModel = defaultModel
	}
	return ep
}

func defaultVendor(displayName, apiKey string, endpoints map[string]EndpointConfig) VendorConfig {
	return VendorConfig{
		DisplayName: displayName,
		APIKey:      apiKey,
		Endpoints:   endpoints,
	}
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
		MaxIterations: 0,
		Vendors: map[string]VendorConfig{
			"zai": defaultVendor("Z.ai", "${ZAI_API_KEY}", map[string]EndpointConfig{
				"cn-coding-openai": defaultEndpoint(
					"CN Coding Plan",
					"openai",
					"https://open.bigmodel.cn/api/coding/paas/v4",
					"glm-5-turbo",
					[]string{"glm-5", "glm-5-turbo", "glm-5.1", "glm-4.7", "glm-4.7-flashx", "glm-4.6", "glm-4.5-air"},
					"coding", "cn",
				),
				"cn-coding-anthropic": defaultEndpoint(
					"CN Coding Plan (Anthropic)",
					"anthropic",
					"https://open.bigmodel.cn/api/anthropic",
					"glm-5-turbo",
					[]string{"glm-5", "glm-5-turbo", "glm-5.1", "glm-4.7", "glm-4.7-flashx", "glm-4.6", "glm-4.5-air"},
					"coding", "cn", "anthropic",
				),
				"global-coding-openai": defaultEndpoint(
					"Global Coding Plan",
					"openai",
					"https://your-global-coding-endpoint.example.com/v1",
					"glm-5-turbo",
					[]string{"glm-5", "glm-5-turbo", "glm-5.1", "glm-4.7", "glm-4.7-flashx", "glm-4.6", "glm-4.5-air"},
					"coding", "global",
				),
				"global-coding-anthropic": defaultEndpoint(
					"Global Coding Plan (Anthropic)",
					"anthropic",
					"https://your-global-anthropic-endpoint.example.com",
					"glm-5-turbo",
					[]string{"glm-5", "glm-5-turbo", "glm-5.1", "glm-4.7", "glm-4.7-flashx", "glm-4.6", "glm-4.5-air"},
					"coding", "global", "anthropic",
				),
				"cn-api-openai": defaultEndpoint(
					"CN Standard API",
					"openai",
					"https://open.bigmodel.cn/api/paas/v4",
					"glm-4.5-air",
					[]string{"glm-5", "glm-5-turbo", "glm-5.1", "glm-4.7", "glm-4.7-flashx", "glm-4.6", "glm-4.5-air"},
					"api", "cn",
				),
				"global-api-openai": defaultEndpoint(
					"Global Standard API",
					"openai",
					"https://your-global-api-endpoint.example.com/v1",
					"glm-4.5-air",
					[]string{"glm-5", "glm-5-turbo", "glm-5.1", "glm-4.7", "glm-4.7-flashx", "glm-4.6", "glm-4.5-air"},
					"api", "global",
				),
			}),
			"anthropic": defaultVendor("Anthropic", "${ANTHROPIC_API_KEY}", map[string]EndpointConfig{
				"api": defaultEndpoint(
					"Anthropic API",
					"anthropic",
					"https://api.anthropic.com",
					"claude-3-5-sonnet-latest",
					[]string{"claude-3-5-sonnet-latest", "claude-3-5-haiku-latest"},
					"official", "anthropic",
				),
			}),
			"openai": defaultVendor("OpenAI", "${OPENAI_API_KEY}", map[string]EndpointConfig{
				"api": defaultEndpoint(
					"OpenAI API",
					"openai",
					"https://api.openai.com/v1",
					"gpt-4o-mini",
					[]string{"gpt-4o-mini", "gpt-4o"},
					"official", "openai",
				),
			}),
			"google": defaultVendor("Google Gemini", "${GEMINI_API_KEY}", map[string]EndpointConfig{
				"api": defaultEndpoint(
					"Gemini API",
					"gemini",
					"https://generativelanguage.googleapis.com",
					"gemini-1.5-flash",
					[]string{"gemini-1.5-flash", "gemini-1.5-pro"},
					"official", "gemini",
				),
			}),
			"openrouter": defaultVendor("OpenRouter", "${OPENROUTER_API_KEY}", map[string]EndpointConfig{
				"api": defaultEndpoint(
					"OpenRouter API",
					"openai",
					"https://openrouter.ai/api/v1",
					"openai/gpt-4o-mini",
					[]string{"openai/gpt-4o-mini", "anthropic/claude-3.5-sonnet", "google/gemini-flash-1.5"},
					"router", "openai-compatible",
				),
			}),
			"groq": defaultVendor("Groq", "${GROQ_API_KEY}", map[string]EndpointConfig{
				"api": defaultEndpoint(
					"Groq API",
					"openai",
					"https://api.groq.com/openai/v1",
					"llama-3.1-8b-instant",
					[]string{"llama-3.1-8b-instant", "llama-3.1-70b-versatile"},
					"official", "openai-compatible", "fast",
				),
			}),
			"mistral": defaultVendor("Mistral", "${MISTRAL_API_KEY}", map[string]EndpointConfig{
				"api": defaultEndpoint(
					"Mistral API",
					"openai",
					"https://api.mistral.ai/v1",
					"mistral-small-latest",
					[]string{"mistral-small-latest", "mistral-large-latest"},
					"official", "openai-compatible",
				),
			}),
			"deepseek": defaultVendor("DeepSeek", "${DEEPSEEK_API_KEY}", map[string]EndpointConfig{
				"api": defaultEndpoint(
					"DeepSeek API",
					"openai",
					"https://api.deepseek.com/v1",
					"deepseek-chat",
					[]string{"deepseek-chat", "deepseek-reasoner"},
					"official", "openai-compatible", "reasoning",
				),
			}),
			"moonshot": defaultVendor("Moonshot AI", "${MOONSHOT_API_KEY}", map[string]EndpointConfig{
				"api": defaultEndpoint(
					"Moonshot API",
					"openai",
					"https://api.moonshot.cn/v1",
					"moonshot-v1-8k",
					[]string{"moonshot-v1-8k", "moonshot-v1-32k"},
					"official", "openai-compatible", "cn",
				),
			}),
			"kimi": defaultVendor("Kimi Coding Plan", "${KIMI_API_KEY}", map[string]EndpointConfig{
				"coding-openai": defaultEndpoint(
					"Kimi Coding Plan",
					"openai",
					"https://api.kimi.com/coding/v1",
					"kimi-for-coding",
					[]string{"kimi-for-coding"},
					"official", "coding", "openai-compatible",
				),
				"coding-anthropic": defaultEndpoint(
					"Kimi Coding Plan (Anthropic)",
					"anthropic",
					"https://api.kimi.com/coding/",
					"kimi-for-coding",
					[]string{"kimi-for-coding"},
					"official", "coding", "anthropic",
				),
			}),
			"minimax": defaultVendor("MiniMax Token Plan", "${MINIMAX_API_KEY}", map[string]EndpointConfig{
				"token-plan-openai": defaultEndpoint(
					"MiniMax Token Plan",
					"openai",
					"https://api.minimaxi.com/v1",
					"MiniMax-M2.7",
					[]string{"MiniMax-M2.7"},
					"official", "coding", "openai-compatible",
				),
				"token-plan-anthropic": defaultEndpoint(
					"MiniMax Token Plan (Anthropic)",
					"anthropic",
					"https://api.minimaxi.com/anthropic",
					"MiniMax-M2.7",
					[]string{"MiniMax-M2.7"},
					"official", "coding", "anthropic",
				),
				"global-openai": defaultEndpoint(
					"MiniMax Global",
					"openai",
					"https://api.minimax.io/v1",
					"MiniMax-M2.7",
					[]string{"MiniMax-M2.7"},
					"official", "coding", "openai-compatible", "global",
				),
				"global-anthropic": defaultEndpoint(
					"MiniMax Global (Anthropic)",
					"anthropic",
					"https://api.minimax.io/anthropic",
					"MiniMax-M2.7",
					[]string{"MiniMax-M2.7"},
					"official", "coding", "anthropic", "global",
				),
			}),
			"ark": defaultVendor("Volcengine Ark Coding Plan", "${ARK_API_KEY}", map[string]EndpointConfig{
				"coding-openai": defaultEndpoint(
					"Ark Coding Plan",
					"openai",
					"https://ark.cn-beijing.volces.com/api/coding/v3",
					"ark-code-latest",
					[]string{"ark-code-latest"},
					"official", "coding", "cn", "openai-compatible",
				),
				"coding-anthropic": defaultEndpoint(
					"Ark Coding Plan (Anthropic)",
					"anthropic",
					"https://ark.cn-beijing.volces.com/api/coding",
					"ark-code-latest",
					[]string{"ark-code-latest"},
					"official", "coding", "cn", "anthropic",
				),
			}),
			"together": defaultVendor("Together AI", "${TOGETHER_API_KEY}", map[string]EndpointConfig{
				"api": defaultEndpoint(
					"Together API",
					"openai",
					"https://api.together.xyz/v1",
					"meta-llama/Meta-Llama-3.1-8B-Instruct-Turbo",
					[]string{
						"meta-llama/Meta-Llama-3.1-8B-Instruct-Turbo",
						"meta-llama/Meta-Llama-3.1-70B-Instruct-Turbo",
					},
					"official", "openai-compatible", "open-models",
				),
			}),
			"perplexity": defaultVendor("Perplexity", "${PERPLEXITY_API_KEY}", map[string]EndpointConfig{
				"api": defaultEndpoint(
					"Perplexity API",
					"openai",
					"https://api.perplexity.ai",
					"llama-3.1-sonar-small-128k-online",
					[]string{"llama-3.1-sonar-small-128k-online", "llama-3.1-sonar-large-128k-online"},
					"official", "openai-compatible", "search",
				),
			}),
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
			cfg.FirstRun = true
			applyFirstLaunchAnthropicBootstrap(cfg)
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
	if shouldApplyFirstLaunchAnthropicBootstrap(raw) {
		applyFirstLaunchAnthropicBootstrap(cfg)
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
		mcp.Type = ExpandEnv(mcp.Type)
		mcp.Command = ExpandEnv(mcp.Command)
		for j, arg := range mcp.Args {
			mcp.Args[j] = ExpandEnv(arg)
		}
		for key, val := range mcp.Env {
			mcp.Env[key] = ExpandEnv(val)
		}
		mcp.URL = ExpandEnv(mcp.URL)
		for key, val := range mcp.Headers {
			mcp.Headers[key] = ExpandEnv(val)
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
	if c.MaxIterations < 0 {
		return fmt.Errorf("max_iterations must not be negative")
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
	for _, mcp := range c.MCPServers {
		transport := strings.ToLower(strings.TrimSpace(mcp.Type))
		if transport == "" {
			transport = "stdio"
		}
		if strings.TrimSpace(mcp.Name) == "" {
			return fmt.Errorf("mcp server name must not be empty")
		}
		switch transport {
		case "stdio":
			if strings.TrimSpace(mcp.Command) == "" {
				return fmt.Errorf("mcp server %q must declare command for stdio transport", mcp.Name)
			}
		case "http", "ws", "websocket":
			if strings.TrimSpace(mcp.URL) == "" {
				return fmt.Errorf("mcp server %q must declare url for %s transport", mcp.Name, transport)
			}
		default:
			return fmt.Errorf("mcp server %q has unsupported transport %q", mcp.Name, transport)
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

func shouldApplyFirstLaunchAnthropicBootstrap(raw map[string]interface{}) bool {
	if len(raw) == 0 {
		return true
	}
	for _, key := range []string{"vendor", "endpoint", "model", "vendors"} {
		if _, ok := raw[key]; ok {
			return false
		}
	}
	return true
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
		maxTokens = inferMaxOutputTokens(model, ep.Protocol)
	}
	contextWindow := ep.ContextWindow
	if contextWindow == 0 {
		contextWindow = inferContextWindow(model, ep.Protocol)
	}
	return &ResolvedEndpoint{
		VendorID:      c.Vendor,
		VendorName:    firstNonEmpty(vc.DisplayName, c.Vendor),
		EndpointID:    c.Endpoint,
		EndpointName:  firstNonEmpty(ep.DisplayName, c.Endpoint),
		Protocol:      ep.Protocol,
		BaseURL:       ep.BaseURL,
		APIKey:        apiKey,
		Model:         model,
		ContextWindow: contextWindow,
		MaxTokens:     maxTokens,
		Models:        append([]string(nil), ep.Models...),
		Tags:          append([]string(nil), ep.Tags...),
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

func (c *Config) UpsertMCPServer(server MCPServerConfig) (replaced bool) {
	if c == nil {
		return false
	}
	for i, existing := range c.MCPServers {
		if existing.Name != server.Name {
			continue
		}
		c.MCPServers[i] = server
		return true
	}
	c.MCPServers = append(c.MCPServers, server)
	return false
}

func (c *Config) RemoveMCPServer(name string) bool {
	if c == nil {
		return false
	}
	for i, server := range c.MCPServers {
		if server.Name != name {
			continue
		}
		c.MCPServers = append(c.MCPServers[:i], c.MCPServers[i+1:]...)
		return true
	}
	return false
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

func (c *Config) SaveLanguagePreference(lang string) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	if strings.TrimSpace(c.FilePath) == "" {
		return fmt.Errorf("config file path is empty")
	}
	lang = strings.TrimSpace(lang)
	if lang == "" {
		return fmt.Errorf("language must not be empty")
	}

	raw := map[string]interface{}{}
	data, err := os.ReadFile(c.FilePath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("reading config %s: %w", c.FilePath, err)
		}
	} else if len(data) > 0 {
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("parsing config %s: %w", c.FilePath, err)
		}
	}
	raw["language"] = lang

	updated, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(c.FilePath), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	if err := os.WriteFile(c.FilePath, updated, 0644); err != nil {
		return err
	}
	c.Language = lang
	c.FirstRun = false
	return nil
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
