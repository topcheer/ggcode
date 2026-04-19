package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/hooks"
	"gopkg.in/yaml.v3"
)

// EndpointConfig describes a concrete vendor endpoint that maps to one protocol.
type EndpointConfig struct {
	DisplayName    string   `yaml:"display_name"`
	Protocol       string   `yaml:"protocol"`
	BaseURL        string   `yaml:"base_url"`
	AuthType       string   `yaml:"auth_type,omitempty"`
	APIKey         string   `yaml:"api_key,omitempty"`
	ContextWindow  int      `yaml:"context_window,omitempty"`
	MaxTokens      int      `yaml:"max_tokens"`
	SupportsVision *bool    `yaml:"supports_vision,omitempty"`
	DefaultModel   string   `yaml:"default_model,omitempty"`
	SelectedModel  string   `yaml:"selected_model,omitempty"`
	Models         []string `yaml:"models,omitempty"`
	Tags           []string `yaml:"tags,omitempty"`
}

// VendorConfig holds a real supplier plus its available endpoints.
type VendorConfig struct {
	DisplayName string                    `yaml:"display_name"`
	APIKey      string                    `yaml:"api_key,omitempty"`
	Endpoints   map[string]EndpointConfig `yaml:"endpoints"`
}

// ResolvedEndpoint is the runtime selection after config inheritance is applied.
type ResolvedEndpoint struct {
	VendorID       string
	VendorName     string
	EndpointID     string
	EndpointName   string
	Protocol       string
	AuthType       string
	BaseURL        string
	APIKey         string
	EnterpriseURL  string
	Model          string
	ContextWindow  int
	MaxTokens      int
	SupportsVision bool
	Models         []string
	Tags           []string
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
	Name              string            `yaml:"name"`
	Type              string            `yaml:"type,omitempty"`
	Command           string            `yaml:"command,omitempty"`
	Args              []string          `yaml:"args,omitempty"`
	Env               map[string]string `yaml:"env,omitempty"`
	URL               string            `yaml:"url,omitempty"`
	Headers           map[string]string `yaml:"headers,omitempty"`
	OAuthClientID     string            `yaml:"oauth_client_id,omitempty" json:"oauth_client_id,omitempty"`
	OAuthClientSecret string            `yaml:"oauth_client_secret,omitempty" json:"oauth_client_secret,omitempty"`
	Source            string            `yaml:"-"`
	OriginPath        string            `yaml:"-"`
	Migrated          bool              `yaml:"-"`
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

## Core behavior
- Be precise, concise, and proactive.
- Prefer small, reversible changes over broad rewrites.
- Read before you edit, and inspect results before claiming success.
- Use ` + "`ask_user`" + ` only when a material clarification is needed, the answer will change what you do next, and there is no safe best guess from the current context.
- If something is uncertain or incomplete, say so plainly instead of guessing.

## Tool routing
- For repository inspection, prefer built-in file and search tools first: ` + "`read_file`" + `, ` + "`list_directory`" + `, ` + "`search_files`" + `, and ` + "`glob`" + `. Do not reach for shell commands when a built-in tool is clearer.
- Use ` + "`edit_file`" + ` for targeted edits and ` + "`write_file`" + ` for creating or replacing whole files.
- Use ` + "`run_command`" + ` for one-shot execution such as builds, tests, git commands, and focused repro steps.
- Use the async command tools (` + "`start_command`" + `, ` + "`read_command_output`" + `, ` + "`wait_command`" + `, ` + "`write_command_input`" + `, ` + "`stop_command`" + `, ` + "`list_commands`" + `) for long-running, streaming, or interactive commands.
- Use ` + "`list_mcp_capabilities`" + ` before assuming MCP-backed browser, external service, or prompt/resource capabilities are available.
- Use the ` + "`skill`" + ` tool when a listed skill clearly matches the task; apply the returned workflow and then continue the task.

## Working style
- Prefer the smallest concrete check that proves the requested behavior.
- Batch related inspections or validations into a single assistant turn when the needed tool calls can be chosen together. Avoid one-tool-at-a-time exploration when several checks are obviously needed.
- Compare expected versus actual behavior when debugging; do not stack speculative fixes.
- Do not emit progress-only assistant messages while meaningful work remains. Continue directly to the next useful tool calls when you already know them.
- Treat ` + "`todo_write`" + ` as optional bookkeeping for genuinely multi-step work. Do not update it after every micro-step; only write todos when the task spans multiple meaningful phases or the plan materially changes.
- Keep user-facing summaries short and useful.
- Use ` + "`@mentions`" + ` when referencing files for context.

## Memory
- Use ` + "`save_memory`" + ` for durable patterns and decisions that will matter later.
- Check project memory files such as ` + "`GGCODE.md`" + `, ` + "`AGENTS.md`" + `, ` + "`CLAUDE.md`" + `, and ` + "`COPILOT.md`" + ` for project-specific guidance.
- Learn from stable user preferences across sessions.

## Git conventions
- Always include "Co-Authored-By: ggcode <noreply@ggcode.dev>" in git commit messages.
- Example: git commit -m "feat: add feature\n\nCo-Authored-By: ggcode <noreply@ggcode.dev>"
`

// Config is the top-level configuration.
type Config struct {
	Vendor        string                    `yaml:"vendor"`
	Endpoint      string                    `yaml:"endpoint"`
	Model         string                    `yaml:"model"`
	Language      string                    `yaml:"language"`
	UI            UIConfig                  `yaml:"ui,omitempty"`
	IM            IMConfig                  `yaml:"im,omitempty"`
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
	Impersonation ImpersonationConfig       `yaml:"impersonation,omitempty"`
	FilePath      string                    `yaml:"-"`
	FirstRun      bool                      `yaml:"-"`
}

// ImpersonationConfig holds persisted impersonation settings.
type ImpersonationConfig struct {
	Preset        string            `yaml:"preset,omitempty"`
	CustomVersion string            `yaml:"custom_version,omitempty"`
	CustomHeaders map[string]string `yaml:"custom_headers,omitempty"`
}

type UIConfig struct {
	SidebarVisible *bool `yaml:"sidebar_visible,omitempty"`
}

type IMConfig struct {
	Enabled             bool                       `yaml:"enabled,omitempty"`
	ActiveSessionPolicy string                     `yaml:"active_session_policy,omitempty"`
	RequireLocalSession *bool                      `yaml:"require_local_session,omitempty"`
	Streaming           IMStreamingConfig          `yaml:"streaming,omitempty"`
	STT                 IMSTTConfig                `yaml:"stt,omitempty"`
	Adapters            map[string]IMAdapterConfig `yaml:"adapters,omitempty"`
}

type IMStreamingConfig struct {
	Enabled         bool    `yaml:"enabled,omitempty"`
	Transport       string  `yaml:"transport,omitempty"`
	EditIntervalSec float64 `yaml:"edit_interval_sec,omitempty"`
	BufferThreshold int     `yaml:"buffer_threshold,omitempty"`
	Cursor          string  `yaml:"cursor,omitempty"`
}

type IMSTTConfig struct {
	Provider string `yaml:"provider,omitempty"`
	BaseURL  string `yaml:"base_url,omitempty"`
	APIKey   string `yaml:"api_key,omitempty"`
	Model    string `yaml:"model,omitempty"`
}

type IMAdapterConfig struct {
	Enabled   bool                   `yaml:"enabled,omitempty"`
	Platform  string                 `yaml:"platform,omitempty"`
	Transport string                 `yaml:"transport,omitempty"`
	Command   string                 `yaml:"command,omitempty"`
	Args      []string               `yaml:"args,omitempty"`
	Env       map[string]string      `yaml:"env,omitempty"`
	AllowFrom []string               `yaml:"allow_from,omitempty"`
	Targets   []IMTargetConfig       `yaml:"targets,omitempty"`
	Extra     map[string]interface{} `yaml:"extra,omitempty"`
}

type IMTargetConfig struct {
	ID       string            `yaml:"id,omitempty"`
	Label    string            `yaml:"label,omitempty"`
	Channel  string            `yaml:"channel,omitempty"`
	Thread   string            `yaml:"thread,omitempty"`
	Metadata map[string]string `yaml:"metadata,omitempty"`
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
		AuthType:      "api_key",
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
					"https://api.z.ai/api/coding/paas/v4",
					"glm-5-turbo",
					[]string{"glm-5", "glm-5-turbo", "glm-5.1", "glm-4.7", "glm-4.7-flashx", "glm-4.6", "glm-4.5-air"},
					"coding", "global",
				),
				"global-coding-anthropic": defaultEndpoint(
					"Global Coding Plan (Anthropic)",
					"anthropic",
					"https://api.z.ai/api/anthropic",
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
					"https://api.z.ai/api/paas/v4",
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
				"oauth": func() EndpointConfig {
					ep := defaultEndpoint(
						"Anthropic OAuth",
						"anthropic",
						"https://api.anthropic.com",
						"claude-sonnet-4-20250514",
						[]string{"claude-sonnet-4-20250514", "claude-opus-4-20250514", "claude-3-5-sonnet-latest", "claude-3-5-haiku-latest"},
						"official", "anthropic", "oauth",
					)
					ep.AuthType = "oauth"
					return ep
				}(),
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
			"aihubmix": defaultVendor("AIHubMix", "${AIHUBMIX_API_KEY}", map[string]EndpointConfig{
				"api": defaultEndpoint(
					"AIHubMix API",
					"openai",
					"https://aihubmix.com/v1",
					"gpt-4o-mini",
					[]string{"gpt-4o-mini"},
					"official", "openai-compatible", "router",
				),
			}),
			"getgoapi": defaultVendor("GetGoAPI", "${GETGOAPI_API_KEY}", map[string]EndpointConfig{
				"api": defaultEndpoint(
					"GetGoAPI API",
					"openai",
					"https://api.getgoapi.com/v1",
					"gpt-4o-mini",
					[]string{"gpt-4o-mini"},
					"official", "openai-compatible", "router",
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
			"novita": defaultVendor("Novita AI", "${NOVITA_API_KEY}", map[string]EndpointConfig{
				"api": defaultEndpoint(
					"Novita AI API",
					"openai",
					"https://api.novita.ai/openai/v1",
					"gpt-4o-mini",
					[]string{"gpt-4o-mini"},
					"official", "openai-compatible", "router",
				),
			}),
			"aliyun": defaultVendor("Aliyun Bailian Coding Plan", "${DASHSCOPE_API_KEY}", map[string]EndpointConfig{
				"coding-openai": defaultEndpoint(
					"Aliyun Bailian Coding Plan",
					"openai",
					"https://coding.dashscope.aliyuncs.com/v1",
					"qwen3-coder-plus",
					[]string{"qwen3-coder-plus"},
					"official", "coding", "cn", "openai-compatible",
				),
				"coding-anthropic": defaultEndpoint(
					"Aliyun Bailian Coding Plan (Anthropic)",
					"anthropic",
					"https://coding.dashscope.aliyuncs.com/apps/anthropic",
					"qwen3-coder-plus",
					[]string{"qwen3-coder-plus"},
					"official", "coding", "cn", "anthropic",
				),
			}),
			"poe": defaultVendor("Poe", "${POE_API_KEY}", map[string]EndpointConfig{
				"api": defaultEndpoint(
					"Poe API",
					"openai",
					"https://api.poe.com/v1",
					"gpt-4o-mini",
					[]string{"gpt-4o-mini"},
					"official", "openai-compatible", "router",
				),
			}),
			"requesty": defaultVendor("Requesty", "${REQUESTY_API_KEY}", map[string]EndpointConfig{
				"api": defaultEndpoint(
					"Requesty API",
					"openai",
					"https://router.requesty.ai/v1",
					"gpt-4o-mini",
					[]string{"gpt-4o-mini"},
					"official", "openai-compatible", "router",
				),
			}),
			"vercel": defaultVendor("Vercel AI Gateway", "${VERCEL_AI_GATEWAY_API_KEY}", map[string]EndpointConfig{
				"api": defaultEndpoint(
					"Vercel AI Gateway",
					"openai",
					"https://ai-gateway.vercel.sh/v1",
					"gpt-4o-mini",
					[]string{"gpt-4o-mini"},
					"official", "openai-compatible", "gateway",
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
			"github-copilot": defaultVendor("GitHub Copilot", "", map[string]EndpointConfig{
				"github.com": func() EndpointConfig {
					ep := defaultEndpoint(
						"GitHub.com",
						"copilot",
						auth.CopilotAPIBaseURL(""),
						"gpt-4o",
						[]string{"gpt-4o", "claude-3.5-sonnet", "gemini-2.0-flash-001"},
						"official", "oauth", "copilot",
					)
					ep.AuthType = "oauth"
					return ep
				}(),
				"enterprise": func() EndpointConfig {
					ep := defaultEndpoint(
						"GitHub Enterprise",
						"copilot",
						auth.CopilotAPIBaseURL("github.example.com"),
						"gpt-4o",
						[]string{"gpt-4o", "claude-3.5-sonnet", "gemini-2.0-flash-001"},
						"official", "oauth", "copilot", "enterprise",
					)
					ep.AuthType = "oauth"
					return ep
				}(),
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
	lookup := runtimeEnvLookup(nil)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg.FirstRun = true
			applyFirstLaunchAnthropicBootstrap(cfg)
			cfg.expandEnvWithLookup(lookup)
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
	lookup = runtimeEnvLookup(raw)
	expanded := ExpandEnvRecursiveWithLookup(raw, lookup)

	// Re-marshal and unmarshal into struct
	expandedData, err := yaml.Marshal(expanded)
	if err != nil {
		return nil, fmt.Errorf("marshaling expanded config: %w", err)
	}

	if err := yaml.Unmarshal(expandedData, cfg); err != nil {
		return nil, fmt.Errorf("parsing expanded config: %w", err)
	}
	mergeDefaultEndpoints(cfg, DefaultConfig())
	migrateLegacyMaxIterations(path, raw, cfg)
	cfg.expandEnvWithLookup(lookup)
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

func migrateLegacyMaxIterations(path string, raw map[string]interface{}, cfg *Config) {
	if cfg == nil || !isDefaultUserConfigPath(path) {
		return
	}
	value, ok := raw["max_iterations"]
	if !ok {
		return
	}
	switch v := value.(type) {
	case int:
		if v == 50 {
			cfg.MaxIterations = 0
		}
	case int64:
		if v == 50 {
			cfg.MaxIterations = 0
		}
	case float64:
		if v == 50 {
			cfg.MaxIterations = 0
		}
	}
}

func isDefaultUserConfigPath(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	return filepath.Clean(path) == filepath.Clean(ConfigPath())
}

func (c *Config) expandEnv() {
	c.expandEnvWithLookup(os.LookupEnv)
}

func (c *Config) expandEnvWithLookup(lookup envLookupFunc) {
	c.Vendor = ExpandEnvWithLookup(c.Vendor, lookup)
	c.Endpoint = ExpandEnvWithLookup(c.Endpoint, lookup)
	c.Model = ExpandEnvWithLookup(c.Model, lookup)
	c.SystemPrompt = ExpandEnvWithLookup(c.SystemPrompt, lookup)
	c.DefaultMode = ExpandEnvWithLookup(c.DefaultMode, lookup)
	for i, dir := range c.AllowedDirs {
		c.AllowedDirs[i] = ExpandEnvWithLookup(dir, lookup)
	}
	for vendorName, vc := range c.Vendors {
		vc.DisplayName = ExpandEnvWithLookup(vc.DisplayName, lookup)
		vc.APIKey = ExpandEnvWithLookup(vc.APIKey, lookup)
		for endpointName, ep := range vc.Endpoints {
			ep.DisplayName = ExpandEnvWithLookup(ep.DisplayName, lookup)
			ep.Protocol = ExpandEnvWithLookup(ep.Protocol, lookup)
			ep.BaseURL = ExpandEnvWithLookup(ep.BaseURL, lookup)
			ep.AuthType = ExpandEnvWithLookup(ep.AuthType, lookup)
			ep.APIKey = ExpandEnvWithLookup(ep.APIKey, lookup)
			ep.DefaultModel = ExpandEnvWithLookup(ep.DefaultModel, lookup)
			ep.SelectedModel = ExpandEnvWithLookup(ep.SelectedModel, lookup)
			for i, model := range ep.Models {
				ep.Models[i] = ExpandEnvWithLookup(model, lookup)
			}
			for i, tag := range ep.Tags {
				ep.Tags[i] = ExpandEnvWithLookup(tag, lookup)
			}
			vc.Endpoints[endpointName] = ep
		}
		c.Vendors[vendorName] = vc
	}
	for i, plugin := range c.Plugins {
		plugin.Name = ExpandEnvWithLookup(plugin.Name, lookup)
		plugin.Path = ExpandEnvWithLookup(plugin.Path, lookup)
		plugin.Type = ExpandEnvWithLookup(plugin.Type, lookup)
		for j, cmd := range plugin.Commands {
			cmd.Name = ExpandEnvWithLookup(cmd.Name, lookup)
			cmd.Description = ExpandEnvWithLookup(cmd.Description, lookup)
			cmd.Execute = ExpandEnvWithLookup(cmd.Execute, lookup)
			for k, arg := range cmd.Args {
				cmd.Args[k] = ExpandEnvWithLookup(arg, lookup)
			}
			plugin.Commands[j] = cmd
		}
		c.Plugins[i] = plugin
	}
	for i, mcp := range c.MCPServers {
		mcp.Name = ExpandEnvWithLookup(mcp.Name, lookup)
		mcp.Type = ExpandEnvWithLookup(mcp.Type, lookup)
		mcp.Command = ExpandEnvWithLookup(mcp.Command, lookup)
		for j, arg := range mcp.Args {
			mcp.Args[j] = ExpandEnvWithLookup(arg, lookup)
		}
		for key, val := range mcp.Env {
			mcp.Env[key] = ExpandEnvWithLookup(val, lookup)
		}
		mcp.URL = ExpandEnvWithLookup(mcp.URL, lookup)
		for key, val := range mcp.Headers {
			mcp.Headers[key] = ExpandEnvWithLookup(val, lookup)
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
	if strings.TrimSpace(ep.AuthType) == "" {
		ep.AuthType = "api_key"
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
	return c.ResolveEndpoint(c.Vendor, c.Endpoint)
}

// ResolveEndpoint resolves the given vendor + endpoint into runtime settings.
func (c *Config) ResolveEndpoint(vendor, endpoint string) (*ResolvedEndpoint, error) {
	if c == nil {
		return nil, fmt.Errorf("config is nil")
	}
	vc, ok := c.Vendors[vendor]
	if !ok {
		return nil, fmt.Errorf("vendor %q is not configured", vendor)
	}
	ep, ok := vc.Endpoints[endpoint]
	if !ok {
		return nil, fmt.Errorf("endpoint %q is not configured for vendor %q", endpoint, vendor)
	}
	model := ""
	if c.Vendor == vendor && c.Endpoint == endpoint {
		model = strings.TrimSpace(c.Model)
	}
	if model == "" {
		model = strings.TrimSpace(ep.SelectedModel)
	}
	if model == "" {
		model = strings.TrimSpace(ep.DefaultModel)
	}
	if model == "" {
		return nil, fmt.Errorf("endpoint %q for vendor %q has no active model", endpoint, vendor)
	}
	apiKey := strings.TrimSpace(ep.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(vc.APIKey)
	}
	authType := strings.TrimSpace(ep.AuthType)
	if authType == "" {
		authType = "api_key"
	}
	baseURL := strings.TrimSpace(ep.BaseURL)
	enterpriseURL := ""
	if authType == "oauth" && vendor == auth.ProviderGitHubCopilot {
		info, err := auth.DefaultStore().Load(auth.ProviderGitHubCopilot)
		if err != nil {
			return nil, err
		}
		if info != nil {
			if apiKey == "" {
				apiKey = strings.TrimSpace(info.AccessToken)
			}
			enterpriseURL = strings.TrimSpace(info.EnterpriseURL)
			if endpoint == "enterprise" && enterpriseURL != "" {
				baseURL = auth.CopilotAPIBaseURL(enterpriseURL)
			} else if endpoint == "github.com" {
				baseURL = auth.CopilotAPIBaseURL("")
			}
		}
	}
	if authType == "oauth" && vendor == auth.ProviderAnthropic {
		info, err := auth.DefaultStore().Load(auth.ProviderAnthropic)
		if err != nil {
			return nil, err
		}
		if info != nil {
			if info.IsExpired() && strings.TrimSpace(info.RefreshToken) != "" {
				refreshed, refreshErr := auth.RefreshClaudeToken(context.Background(), info.RefreshToken)
				if refreshErr == nil && refreshed != nil {
					_ = auth.DefaultStore().Save(refreshed)
					apiKey = strings.TrimSpace(refreshed.AccessToken)
				} else {
					apiKey = strings.TrimSpace(info.AccessToken)
				}
			} else {
				apiKey = strings.TrimSpace(info.AccessToken)
			}
		}
	}
	if baseURL == "" {
		return nil, fmt.Errorf("endpoint %q for vendor %q has no base_url configured", endpoint, vendor)
	}
	maxTokens := ep.MaxTokens
	if maxTokens == 0 {
		maxTokens = inferMaxOutputTokens(model, ep.Protocol)
	}
	contextWindow := ep.ContextWindow
	if contextWindow == 0 {
		contextWindow = inferContextWindow(model, ep.Protocol)
	}
	supportsVision := inferVisionSupport(model, ep.Protocol)
	if ep.SupportsVision != nil {
		supportsVision = *ep.SupportsVision
	}
	return &ResolvedEndpoint{
		VendorID:       vendor,
		VendorName:     firstNonEmpty(vc.DisplayName, vendor),
		EndpointID:     endpoint,
		EndpointName:   firstNonEmpty(ep.DisplayName, endpoint),
		Protocol:       ep.Protocol,
		AuthType:       authType,
		BaseURL:        baseURL,
		APIKey:         apiKey,
		EnterpriseURL:  enterpriseURL,
		Model:          model,
		ContextWindow:  contextWindow,
		MaxTokens:      maxTokens,
		SupportsVision: supportsVision,
		Models:         append([]string(nil), ep.Models...),
		Tags:           append([]string(nil), ep.Tags...),
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
// The key is stored as an environment variable reference (e.g. ${ZAI_API_KEY})
// rather than plaintext, and the caller should set the actual value in the
// shell environment (os.Setenv) so the current session can use it immediately.
func (c *Config) SetEndpointAPIKey(vendor, endpoint, apiKey string, vendorScoped bool) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	vc, ok := c.Vendors[vendor]
	if !ok {
		return fmt.Errorf("vendor %q is not configured", vendor)
	}

	apiKey = strings.TrimSpace(apiKey)

	// If the value is already an env reference (${VAR}), store as-is.
	if _, isRef := envReferenceVarName(apiKey); isRef || apiKey == "" {
		if vendorScoped {
			vc.APIKey = apiKey
			c.Vendors[vendor] = vc
		} else {
			ep, ok := vc.Endpoints[endpoint]
			if !ok {
				return fmt.Errorf("endpoint %q is not configured for vendor %q", endpoint, vendor)
			}
			ep.APIKey = apiKey
			vc.Endpoints[endpoint] = ep
			c.Vendors[vendor] = vc
		}
		return nil
	}

	// Plaintext key: resolve the preferred env var name and store the reference.
	var envVarName string
	if vendorScoped {
		envVarName = preferredVendorAPIKeyEnvVar(vendor)
	} else {
		envVarName = preferredEndpointAPIKeyEnvVar(vendor, endpoint)
	}

	// Set the actual value in the current process environment so it works
	// immediately for the current session.
	os.Setenv(envVarName, apiKey)

	ref := "${" + envVarName + "}"
	if vendorScoped {
		vc.APIKey = ref
		c.Vendors[vendor] = vc
	} else {
		ep, ok := vc.Endpoints[endpoint]
		if !ok {
			return fmt.Errorf("endpoint %q is not configured for vendor %q", endpoint, vendor)
		}
		ep.APIKey = ref
		vc.Endpoints[endpoint] = ep
		c.Vendors[vendor] = vc
	}
	return nil
}

// SetEndpointModels replaces the known models for a configured endpoint while preserving active selections.
func (c *Config) SetEndpointModels(vendor, endpoint string, models []string) error {
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
	ep.Models = uniqueNonEmptyStrings(append(models, ep.SelectedModel, ep.DefaultModel)...)
	vc.Endpoints[endpoint] = ep
	c.Vendors[vendor] = vc
	if c.Vendor == vendor && c.Endpoint == endpoint {
		c.normalizeActiveModel()
	}
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

// SaveImpersonation persists impersonation settings to the config file.
func (c *Config) SaveImpersonation(imp ImpersonationConfig) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	if strings.TrimSpace(c.FilePath) == "" {
		return fmt.Errorf("config file path is empty")
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

	if imp.Preset == "" && imp.CustomVersion == "" && len(imp.CustomHeaders) == 0 {
		delete(raw, "impersonation")
	} else {
		impMap := map[string]interface{}{}
		if imp.Preset != "" {
			impMap["preset"] = imp.Preset
		}
		if imp.CustomVersion != "" {
			impMap["custom_version"] = imp.CustomVersion
		}
		if len(imp.CustomHeaders) > 0 {
			impMap["custom_headers"] = imp.CustomHeaders
		}
		raw["impersonation"] = impMap
	}

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
	c.Impersonation = imp
	return nil
}

func (c *Config) SidebarVisible() bool {
	if c == nil || c.UI.SidebarVisible == nil {
		return true
	}
	return *c.UI.SidebarVisible
}

func (c *Config) SaveSidebarPreference(visible bool) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	if strings.TrimSpace(c.FilePath) == "" {
		return fmt.Errorf("config file path is empty")
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
	uiRaw, _ := raw["ui"].(map[string]interface{})
	if uiRaw == nil {
		uiRaw = map[string]interface{}{}
	}
	uiRaw["sidebar_visible"] = visible
	raw["ui"] = uiRaw

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
	c.UI.SidebarVisible = boolPtr(visible)
	c.FirstRun = false
	return nil
}

func (c *Config) SaveDefaultModePreference(mode string) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	if strings.TrimSpace(c.FilePath) == "" {
		return fmt.Errorf("config file path is empty")
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "supervised", "plan", "auto", "bypass", "autopilot":
	default:
		return fmt.Errorf("default_mode %q must be one of supervised, plan, auto, bypass, autopilot", mode)
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
	raw["default_mode"] = mode

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
	c.DefaultMode = mode
	c.FirstRun = false
	return nil
}

func (c *Config) AddIMTarget(adapterName string, target IMTargetConfig) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	adapterName = strings.TrimSpace(adapterName)
	if adapterName == "" {
		return fmt.Errorf("adapter name is required")
	}
	if c.IM.Adapters == nil {
		return fmt.Errorf("IM adapters are not configured")
	}
	adapter, ok := c.IM.Adapters[adapterName]
	if !ok {
		return fmt.Errorf("IM adapter %q is not configured", adapterName)
	}
	target.ID = strings.TrimSpace(target.ID)
	target.Label = strings.TrimSpace(target.Label)
	target.Channel = strings.TrimSpace(target.Channel)
	target.Thread = strings.TrimSpace(target.Thread)
	if target.ID == "" {
		return fmt.Errorf("target id is required")
	}
	if target.Channel == "" {
		return fmt.Errorf("channel is required")
	}
	replaced := false
	for i := range adapter.Targets {
		if strings.EqualFold(strings.TrimSpace(adapter.Targets[i].ID), target.ID) {
			adapter.Targets[i] = target
			replaced = true
			break
		}
	}
	if !replaced {
		adapter.Targets = append(adapter.Targets, target)
	}
	c.IM.Adapters[adapterName] = adapter
	return c.Save()
}

func (c *Config) AddIMAdapter(name string, adapter IMAdapterConfig) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("adapter name is required")
	}
	adapter.Platform = strings.TrimSpace(adapter.Platform)
	if adapter.Platform == "" {
		return fmt.Errorf("adapter platform is required")
	}
	if c.IM.Adapters == nil {
		c.IM.Adapters = make(map[string]IMAdapterConfig)
	}
	if _, exists := c.IM.Adapters[name]; exists {
		return fmt.Errorf("IM adapter %q already exists", name)
	}
	c.IM.Adapters[name] = adapter
	return c.Save()
}

// RemoveIMAdapter removes an IM adapter from the configuration.
func (c *Config) RemoveIMAdapter(name string) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("adapter name is required")
	}
	if c.IM.Adapters == nil {
		return fmt.Errorf("IM adapter %q not found", name)
	}
	if _, exists := c.IM.Adapters[name]; !exists {
		return fmt.Errorf("IM adapter %q not found", name)
	}
	delete(c.IM.Adapters, name)
	return c.Save()
}

// SetIMAdapterEnabled toggles the enabled state of an IM adapter.
func (c *Config) SetIMAdapterEnabled(name string, enabled bool) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	name = strings.TrimSpace(name)
	if c.IM.Adapters == nil {
		return fmt.Errorf("IM adapter %q not found", name)
	}
	adapter, ok := c.IM.Adapters[name]
	if !ok {
		return fmt.Errorf("IM adapter %q not found", name)
	}
	adapter.Enabled = enabled
	c.IM.Adapters[name] = adapter
	return c.Save()
}

// SetIMAdapterExtra sets a single key in the adapter's Extra map.
func (c *Config) SetIMAdapterExtra(name, key, value string) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	name = strings.TrimSpace(name)
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("key is required")
	}
	if c.IM.Adapters == nil {
		return fmt.Errorf("IM adapter %q not found", name)
	}
	adapter, ok := c.IM.Adapters[name]
	if !ok {
		return fmt.Errorf("IM adapter %q not found", name)
	}
	if adapter.Extra == nil {
		adapter.Extra = make(map[string]interface{})
	}
	adapter.Extra[key] = value
	c.IM.Adapters[name] = adapter
	return c.Save()
}

func boolPtr(v bool) *bool {
	return &v
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func uniqueNonEmptyStrings(values ...string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

// BuildSystemPrompt enhances the base system prompt with runtime context.
func BuildSystemPrompt(basePrompt, workingDir, language string, toolNames []string, gitStatus string, customCmds []string) string {
	if basePrompt == "" {
		basePrompt = DefaultSystemPrompt
	}
	toolNames = append([]string(nil), toolNames...)
	sort.Strings(toolNames)
	customCmds = append([]string(nil), customCmds...)
	sort.Strings(customCmds)

	var sb strings.Builder
	sb.WriteString(basePrompt)

	if replyLanguageGuidance := buildReplyLanguageGuidance(language); replyLanguageGuidance != "" {
		sb.WriteString("\n\n## Reply Language\n")
		sb.WriteString(replyLanguageGuidance)
		sb.WriteString("\n")
	}

	sb.WriteString("\n\n## Environment\n")
	sb.WriteString(fmt.Sprintf("- Working directory: %s\n", workingDir))
	sb.WriteString(fmt.Sprintf("- OS: %s/%s\n", runtime.GOOS, runtime.GOARCH))
	sb.WriteString(fmt.Sprintf("- Tool schemas are attached separately. Available tools: %s\n", summarizeNames(toolNames, 12)))

	if hasAnyToolPrefix(toolNames, "lsp_") {
		sb.WriteString("\n## LSP Guidance\n")
		sb.WriteString("- If lsp_* tools are available and the user asks about symbol definitions, references, hover/type information, diagnostics, rename, code actions, or workspace symbol lookup in a supported source file, prefer lsp_* tools before broad text search.\n")
		sb.WriteString("- If you know a symbol name but not its exact position, use lsp_symbols or lsp_workspace_symbols first to obtain the precise line/character range, then call lsp_definition, lsp_references, or lsp_hover with that position.\n")
		sb.WriteString("- When several LSP checks are obviously needed, batch them into one turn instead of alternating single LSP calls with new model turns.\n")
		sb.WriteString("- Use read_file or search tools after LSP when you need extra surrounding context or when LSP is unavailable for that file.\n")
	}

	if gitStatus != "" {
		sb.WriteString(fmt.Sprintf("- Git: %s\n", gitStatus))
	}

	if len(customCmds) > 0 {
		sb.WriteString(fmt.Sprintf("- Custom slash commands: %s\n", summarizeNames(customCmds, 8)))
	}

	return sb.String()
}

func summarizeNames(names []string, limit int) string {
	if len(names) == 0 {
		return "none"
	}
	if limit <= 0 || len(names) <= limit {
		return strings.Join(names, ", ")
	}
	head := append([]string(nil), names[:limit]...)
	return fmt.Sprintf("%s (+%d more)", strings.Join(head, ", "), len(names)-limit)
}

func buildReplyLanguageGuidance(language string) string {
	switch normalizedConfigLanguage(language) {
	case "zh-CN":
		return "- Default to Simplified Chinese for user-facing replies because the configured interface language is Simplified Chinese.\n- If the user's current request clearly asks in another language or explicitly requests a different reply language, follow the user's current request for that turn."
	default:
		return "- Default to English for user-facing replies because the configured interface language is English.\n- If the user's current request clearly asks in another language or explicitly requests a different reply language, follow the user's current request for that turn."
	}
}

func normalizedConfigLanguage(language string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "zh", "zh-cn", "zh_hans", "zh-hans", "cn", "zh-sg":
		return "zh-CN"
	default:
		return "en"
	}
}

func hasAnyToolPrefix(toolNames []string, prefix string) bool {
	for _, name := range toolNames {
		if strings.HasPrefix(strings.TrimSpace(name), prefix) {
			return true
		}
	}
	return false
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

// mergeDefaultEndpoints merges endpoints from defaults that are missing in cfg.
// This ensures new built-in endpoints (e.g. "oauth") are available even when
// the user has an existing config file that doesn't include them.
func mergeDefaultEndpoints(cfg, defaults *Config) {
	if cfg == nil || defaults == nil {
		return
	}
	for vendorName, defaultVC := range defaults.Vendors {
		cfgVC, ok := cfg.Vendors[vendorName]
		if !ok {
			continue
		}
		if cfgVC.Endpoints == nil {
			cfgVC.Endpoints = map[string]EndpointConfig{}
		}
		for epName, defaultEP := range defaultVC.Endpoints {
			if _, exists := cfgVC.Endpoints[epName]; !exists {
				cfgVC.Endpoints[epName] = defaultEP
			}
		}
		cfg.Vendors[vendorName] = cfgVC
	}
}
