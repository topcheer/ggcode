package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/stream"
	"gopkg.in/yaml.v3"
)

// configFileLocks serializes read-modify-write operations against a given
// config file path. Multiple goroutines (TUI Bubble Tea cmds, agent loop,
// OAuth refresh, IM bindings) can call Save*/Add*/Remove* concurrently;
// without this lock the read-modify-write sequence races and can drop
// fields, and a crash mid-write can truncate the YAML.
var (
	configFileLocksMu sync.Mutex
	configFileLocks   = map[string]*sync.Mutex{}
)

func lockConfigFile(path string) func() {
	if path == "" {
		return func() {}
	}
	configFileLocksMu.Lock()
	mu, ok := configFileLocks[path]
	if !ok {
		mu = &sync.Mutex{}
		configFileLocks[path] = mu
	}
	configFileLocksMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// EndpointConfig describes a concrete vendor endpoint that maps to one protocol.
type EndpointConfig struct {
	DisplayName     string   `yaml:"display_name" json:"display_name"`
	Protocol        string   `yaml:"protocol" json:"protocol"`
	BaseURL         string   `yaml:"base_url" json:"base_url"`
	AuthType        string   `yaml:"auth_type,omitempty" json:"auth_type,omitempty"`
	APIKey          string   `yaml:"api_key,omitempty" json:"api_key,omitempty"`
	ContextWindow   int      `yaml:"context_window,omitempty" json:"context_window,omitempty"`
	MaxTokens       int      `yaml:"max_tokens" json:"max_tokens"`
	ReasoningEffort string   `yaml:"reasoning_effort,omitempty" json:"reasoning_effort,omitempty"`
	SupportsVision  *bool    `yaml:"supports_vision,omitempty" json:"supports_vision,omitempty"`
	DefaultModel    string   `yaml:"default_model,omitempty" json:"default_model,omitempty"`
	SelectedModel   string   `yaml:"selected_model,omitempty" json:"selected_model,omitempty"`
	Models          []string `yaml:"models,omitempty" json:"models,omitempty"`
	Tags            []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// VendorConfig holds a real supplier plus its available endpoints.
type VendorConfig struct {
	DisplayName string                    `yaml:"display_name" json:"display_name"`
	APIKey      string                    `yaml:"api_key,omitempty" json:"api_key,omitempty"`
	Endpoints   map[string]EndpointConfig `yaml:"endpoints" json:"endpoints"`
}

// ResolvedEndpoint is the runtime selection after config inheritance is applied.
type ResolvedEndpoint struct {
	VendorID        string
	VendorName      string
	EndpointID      string
	EndpointName    string
	Protocol        string
	AuthType        string
	BaseURL         string
	APIKey          string
	EnterpriseURL   string
	Model           string
	ContextWindow   int
	MaxTokens       int
	ReasoningEffort string
	SupportsVision  bool
	Models          []string
	Tags            []string
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
	Name              string            `yaml:"name" json:"name"`
	Type              string            `yaml:"type,omitempty" json:"type,omitempty"`
	Command           string            `yaml:"command,omitempty" json:"command,omitempty"`
	Args              []string          `yaml:"args,omitempty" json:"args,omitempty"`
	Env               map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	URL               string            `yaml:"url,omitempty" json:"url,omitempty"`
	Headers           map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	OAuthClientID     string            `yaml:"oauth_client_id,omitempty" json:"oauth_client_id,omitempty"`
	OAuthClientSecret string            `yaml:"oauth_client_secret,omitempty" json:"oauth_client_secret,omitempty"`
	Source            string            `yaml:"-" json:"-"`
	OriginPath        string            `yaml:"-" json:"-"`
	Migrated          bool              `yaml:"-" json:"-"`
}

// PluginConfigEntry describes a single plugin from the config file.
type PluginConfigEntry struct {
	Name     string                 `yaml:"name"`
	Path     string                 `yaml:"path"`
	Type     string                 `yaml:"type"` // "command", "so", "grpc"
	Commands []PluginCommandConfig  `yaml:"commands"`
	Command  []string               `yaml:"command"` // gRPC plugin: ["python", "-m", "my_plugin"]
	Env      map[string]string      `yaml:"env"`     // gRPC plugin: environment variables
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
- Follow user instructions strictly and completely. Never skip steps, take shortcuts, or substitute your own judgment for explicit user requirements.

## Dependencies
- Always use the latest version of third-party libraries (` + "`go get pkg@latest`" + `). Never pin or accept outdated versions when a newer one is available.
- Minimize new dependencies. Before adding one, check if an existing dependency already provides the needed functionality.

## Tool routing
- For repository inspection, prefer built-in file and search tools first: ` + "`read_file`" + `, ` + "`list_directory`" + `, ` + "`search_files`" + `, and ` + "`glob`" + `. Do not reach for shell commands when a built-in tool is clearer.
- Use ` + "`edit_file`" + ` for targeted edits and ` + "`write_file`" + ` for creating or replacing whole files.
- Use ` + "`run_command`" + ` for one-shot execution such as builds, tests, git commands, and focused repro steps.
- Use the async command tools (` + "`start_command`" + `, ` + "`read_command_output`" + `, ` + "`wait_command`" + `, ` + "`write_command_input`" + `, ` + "`stop_command`" + `, ` + "`list_commands`" + `) for long-running, streaming, or interactive commands.
- Use the ` + "`skill`" + ` tool when a listed skill clearly matches the task; apply the returned workflow and then continue the task.

## Working style
- Prefer the smallest concrete check that proves the requested behavior.
- Batch related inspections or validations into a single assistant turn when the needed tool calls can be chosen together. Avoid one-tool-at-a-time exploration when several checks are obviously needed.
- Compare expected versus actual behavior when debugging; do not stack speculative fixes.
- Reuse existing helpers, patterns, and abstractions before adding new ones. Extend the current design when it already fits.
- Fix the root cause when you can identify it, not just the visible symptom.
- Preserve existing behavior, APIs, and UX unless the user asks for a change. When behavior must change, update all affected paths consistently.
- After code changes, run the narrowest existing validation that proves the change works, then widen validation when the scope or risk is higher.
- Do not emit progress-only assistant messages while meaningful work remains. Continue directly to the next useful tool calls when you already know them.
- Treat ` + "`todo_write`" + ` as optional bookkeeping for genuinely multi-step work. Do not update it after every micro-step; only write todos when the task spans multiple meaningful phases or the plan materially changes.
- Keep user-facing summaries short and useful.
- Do not use emoji with Variation Selector-16 (VS16, U+FE0F) in your output, including tool descriptions, tool call arguments, and assistant messages. These characters (e.g. ⚠️ ✨️ ⚙️ ⭐️ ⏰️ 🔒️ 🔑️) cause terminal rendering alignment issues. Use plain text equivalents instead (e.g. "Warning:", "Note:", "Info:").

## Permission modes
- You can switch between permission modes at any time using the ` + "`switch_mode`" + ` tool. It is always available, even in plan mode.
- Modes: ` + "`supervised`" + ` (default; respects per-tool rules, asks for unspecified), ` + "`plan`" + ` (read-only exploration), ` + "`auto`" + ` (safe ops auto-allowed, dangerous denied), ` + "`bypass`" + ` (almost everything allowed), ` + "`autopilot`" + ` (bypass + autonomous continuation + goal-directed).
- Default to ` + "`supervised`" + ` or ` + "`auto`" + `. Only switch to ` + "`bypass`" + ` or ` + "`autopilot`" + ` when the user explicitly requests it. Switch to ` + "`plan`" + ` when exploring unfamiliar code.

## Memory
- Use ` + "`save_memory`" + ` for durable patterns and decisions that will matter later.
- Check project memory files such as ` + "`GGCODE.md`" + `, ` + "`AGENTS.md`" + `, ` + "`CLAUDE.md`" + `, and ` + "`COPILOT.md`" + ` for project-specific guidance.

## Git conventions
- Always include "Co-Authored-By: ggcode <noreply@ggcode.dev>" in git commit messages.

## Collaboration routing
There are several types of collaborators available. Choose the right one:
- ` + "`spawn_agent`" + ` + ` + "`wait_agent`" + `: Isolated one-shot sub-agent in YOUR workspace. Simplest parallelism — no team or LAN needed. Use for independent investigation, research, or code tasks.
- ` + "`teammate_spawn`" + ` + ` + "`swarm_task_create`" + ` + ` + "`send_message`" + `: Persistent swarm teammate in YOUR workspace. Shares your task board. Use ` + "`swarm_task_create`" + ` with assignee for tracked work, ` + "`send_message`" + ` for lightweight follow-ups. Check results via ` + "`teammate_results`" + `.
- ` + "`lanchat`" + `: Other ggcode instances on the LAN. Use ` + "`action=list`" + ` to see who is online, ` + "`action=send`" + ` to DM a specific person, ` + "`action=set_identity`" + ` to change your own nick/role/team. Prefer idle same-team members (check ` + "`agent_busy`" + `). This is the PRIMARY tool for real-time coordination with other users and their agents.
- ` + "`a2a_remote`" + `: Fire-and-forget headless code editing in another workspace (e.g. "edit file X in project Y", "run tests in project Z"). Not for asking questions.
- ` + "`delegate`" + `: ONLY when the user explicitly names an external CLI agent (e.g. "let claude do it", "ask codex").

Proactive parallelism: when you identify 3+ independent, parallelizable tasks, distribute them immediately rather than doing everything sequentially. Use ` + "`spawn_agent`" + ` for tasks in your workspace, or ` + "`lanchat`" + ` DM to idle same-team members for tasks in their workspaces. After distributing, continue doing remaining work yourself.

Antinoise rules (for lanchat/swarm):
- Prefer targeted DMs over broadcasts. Never broadcast unless the user explicitly asks to notify everyone.
- Do NOT send acknowledgment messages ("got it", "will do", "thanks"). Respond only with meaningful results or stay silent and do the work.
- One message per task. Do NOT send follow-up pings. Check ` + "`action=list`" + ` or wait for results.
- If you receive a broadcast or team message not directed at you specifically, do NOT reply unless you have actionable information.
- When a remote agent goes offline or a2a_remote fails, do NOT silently fall back — use lanchat to coordinate.
- When in doubt, a targeted DM to the specific person is the safe default.
`

// Config is the top-level configuration.
type Config struct {
	Vendor         string                     `yaml:"vendor" json:"vendor"`
	Endpoint       string                     `yaml:"endpoint" json:"endpoint"`
	Model          string                     `yaml:"model" json:"model"`
	Language       string                     `yaml:"language" json:"language"`
	UI             UIConfig                   `yaml:"ui,omitempty" json:"ui,omitempty"`
	IM             IMConfig                   `yaml:"im,omitempty" json:"im,omitempty"`
	ExtraPrompt    string                     `yaml:"extra_prompt" json:"extra_prompt"`
	Vendors        map[string]VendorConfig    `yaml:"vendors" json:"vendors"`
	AllowedDirs    []string                   `yaml:"allowed_dirs" json:"allowed_dirs"`
	MaxIterations  int                        `yaml:"max_iterations" json:"max_iterations"`
	ToolPerms      map[string]ToolPermission  `yaml:"tool_permissions" json:"tool_permissions"`
	Plugins        []PluginConfigEntry        `yaml:"plugins" json:"plugins"`
	MCPServers     []MCPServerConfig          `yaml:"mcp_servers" json:"mcp_servers"`
	Hooks          hooks.HookConfig           `yaml:"hooks" json:"hooks"`
	DefaultMode    string                     `yaml:"default_mode" json:"default_mode"`
	SubAgents      SubAgentConfig             `yaml:"subagents" json:"subagents"`
	Impersonation  ImpersonationConfig        `yaml:"impersonation,omitempty" json:"impersonation,omitempty"`
	KnightConfig   KnightConfig               `yaml:"knight,omitempty" json:"knight,omitempty"`
	Swarm          SwarmConfig                `yaml:"swarm,omitempty" json:"swarm,omitempty"`
	A2A            A2AConfig                  `yaml:"a2a,omitempty" json:"a2a,omitempty"`
	Harness        HarnessConfig              `yaml:"harness,omitempty" json:"harness,omitempty"`
	Stream         stream.StreamConfig        `yaml:"stream,omitempty" json:"stream,omitempty"`
	LSPServers     map[string]LSPServerConfig `yaml:"lsp_servers,omitempty" json:"lsp_servers,omitempty"`
	ProbeContext   bool                       `yaml:"probe_context,omitempty" json:"probe_context,omitempty"`
	FilePath       string                     `yaml:"-" json:"-"`
	FirstRun       bool                       `yaml:"-" json:"-"`
	instanceDir    string                     `yaml:"-" json:"-"` // ~/.ggcode/instances/{sha256}/
	instancePath   string                     `yaml:"-" json:"-"` // instanceDir + "/ggcode.yaml"
	instanceWS     string                     `yaml:"-" json:"-"` // workspace path for SaveInstance
	saveScope      string                     `yaml:"-" json:"-"` // current save scope: "global" or "instance"
	globalSnap     *Config                    `yaml:"-" json:"-"` // deep copy of global config before instance merge
	instanceFields map[string]bool            `yaml:"-" json:"-"` // fields that were filled by instance config
}

// ImpersonationConfig holds persisted impersonation settings.
type ImpersonationConfig struct {
	Preset        string            `yaml:"preset,omitempty"`
	CustomVersion string            `yaml:"custom_version,omitempty"`
	CustomHeaders map[string]string `yaml:"custom_headers,omitempty"`
}

// LSPServerConfig allows users to override the auto-detected LSP server binary
// and arguments for a specific language. The key is the language ID (e.g. "go",
// "rust", "typescript", "python").
type LSPServerConfig struct {
	Binary string   `yaml:"binary,omitempty" json:"binary,omitempty"`
	Args   []string `yaml:"args,omitempty" json:"args,omitempty"`
}

type UIConfig struct {
	SidebarVisible *bool `yaml:"sidebar_visible,omitempty"`
}

type IMConfig struct {
	Enabled             bool                       `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	ActiveSessionPolicy string                     `yaml:"active_session_policy,omitempty" json:"active_session_policy,omitempty"`
	RequireLocalSession *bool                      `yaml:"require_local_session,omitempty" json:"require_local_session,omitempty"`
	OutputMode          string                     `yaml:"output_mode,omitempty" json:"output_mode,omitempty"` // verbose, quiet, summary (default: verbose)
	Streaming           IMStreamingConfig          `yaml:"streaming,omitempty" json:"streaming,omitempty"`
	STT                 IMSTTConfig                `yaml:"stt,omitempty" json:"stt,omitempty"`
	Adapters            map[string]IMAdapterConfig `yaml:"adapters,omitempty" json:"adapters,omitempty"`
}

type IMStreamingConfig struct {
	Enabled         bool    `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Transport       string  `yaml:"transport,omitempty" json:"transport,omitempty"`
	EditIntervalSec float64 `yaml:"edit_interval_sec,omitempty" json:"edit_interval_sec,omitempty"`
	BufferThreshold int     `yaml:"buffer_threshold,omitempty" json:"buffer_threshold,omitempty"`
	Cursor          string  `yaml:"cursor,omitempty" json:"cursor,omitempty"`
}

type IMSTTConfig struct {
	Provider string `yaml:"provider,omitempty" json:"provider,omitempty"`
	BaseURL  string `yaml:"base_url,omitempty" json:"base_url,omitempty"`
	APIKey   string `yaml:"api_key,omitempty" json:"api_key,omitempty"`
	Model    string `yaml:"model,omitempty" json:"model,omitempty"`
}

type IMAdapterConfig struct {
	Enabled    bool                   `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Platform   string                 `yaml:"platform,omitempty" json:"platform,omitempty"`
	Transport  string                 `yaml:"transport,omitempty" json:"transport,omitempty"`
	Command    string                 `yaml:"command,omitempty" json:"command,omitempty"`
	Args       []string               `yaml:"args,omitempty" json:"args,omitempty"`
	Env        map[string]string      `yaml:"env,omitempty" json:"env,omitempty"`
	AllowFrom  []string               `yaml:"allow_from,omitempty" json:"allow_from,omitempty"`
	OutputMode string                 `yaml:"output_mode,omitempty" json:"output_mode,omitempty"` // adapter-level override: verbose, quiet, summary
	Targets    []IMTargetConfig       `yaml:"targets,omitempty" json:"targets,omitempty"`
	Extra      map[string]interface{} `yaml:"extra,omitempty" json:"extra,omitempty"`
}

type IMTargetConfig struct {
	ID       string            `yaml:"id,omitempty" json:"id,omitempty"`
	Label    string            `yaml:"label,omitempty" json:"label,omitempty"`
	Channel  string            `yaml:"channel,omitempty" json:"channel,omitempty"`
	Thread   string            `yaml:"thread,omitempty" json:"thread,omitempty"`
	Metadata map[string]string `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}

// SubAgentConfig holds sub-agent configuration.
type SubAgentConfig struct {
	MaxConcurrent int           `yaml:"max_concurrent"`
	Timeout       time.Duration `yaml:"timeout"`
	ShowOutput    bool          `yaml:"show_output"`
}

// SwarmConfig holds swarm/team multi-agent configuration.
type SwarmConfig struct {
	MaxTeammatesPerTeam int           `yaml:"max_teammates_per_team"` // default: 5
	TeammateTimeout     time.Duration `yaml:"teammate_timeout"`       // default: 0 (no timeout, run until task completes)
	InboxSize           int           `yaml:"inbox_size"`             // default: 32
	PollInterval        time.Duration `yaml:"poll_interval"`          // default: 1s — how often idle teammates check the task board
}

// DefaultA2AAPIKey is a well-known key baked into every ggcode binary.
// It is NOT a secret — its purpose is to ensure that only ggcode instances
// (not random HTTP clients) can reach the A2A endpoint.
//
// For real security, teams should set a2a.auth.api_key to their own value.
const DefaultA2AAPIKey = "ggcode-lan-a2a-v1"

// A2AConfig holds A2A protocol server configuration.
// A2A is enabled by default — mDNS discovery runs automatically so teams
// on the same network can discover each other without any configuration.
type A2AConfig struct {
	Disabled    bool          `yaml:"disabled,omitempty"`   // true to disable (default: enabled)
	Port        int           `yaml:"port"`                 // 0 = auto-assign
	Host        string        `yaml:"host"`                 // default "0.0.0.0" (always)
	MaxTasks    int           `yaml:"max_tasks"`            // concurrent task limit (default 5)
	TaskTimeout string        `yaml:"task_timeout"`         // per-task timeout (default "5m")
	Interfaces  []string      `yaml:"interfaces,omitempty"` // mDNS advertise interfaces (default: auto-detect default route)
	Auth        A2AAuthConfig `yaml:"auth,omitempty"`
}

// HasAuth returns true if at least one authentication mechanism is configured.
func (c A2AConfig) HasAuth() bool {
	return strings.TrimSpace(c.Auth.APIKey) != "" ||
		len(c.Auth.APIKeys) > 0 ||
		c.Auth.OAuth2 != nil ||
		c.Auth.OIDC != nil ||
		c.Auth.MTLS != nil
}

// isLoopbackHost reports whether the given host string is a loopback address
// (127.0.0.1, ::1, localhost). Such addresses make the A2A server unreachable
// from the LAN, breaking mDNS peer discovery and LAN Chat.
func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}

// EffectiveAPIKey returns the user-configured API key if set, otherwise
// the built-in default key. This ensures that even without explicit auth
// config, ggcode instances can authenticate to each other on a LAN.
func (c A2AConfig) EffectiveAPIKey() string {
	if strings.TrimSpace(c.Auth.APIKey) != "" {
		return c.Auth.APIKey
	}
	return DefaultA2AAPIKey
}

// HarnessConfig controls automatic harness routing behavior.
// AutoRun modes:
//   - "off":     No automatic routing (default). Users run harness explicitly.
//   - "suggest": Detect code-change tasks and prompt the user to use harness.
//   - "on":      Automatically route detected code-change tasks to harness.
//   - "strict":  Same as "on" but enforces worktree isolation; blocks direct file writes.
type HarnessConfig struct {
	AutoRun  string `yaml:"auto_run,omitempty" json:"auto_run,omitempty"`   // "off", "suggest", "on", "strict"
	AutoInit bool   `yaml:"auto_init,omitempty" json:"auto_init,omitempty"` // auto-create harness.yaml when missing
}

// AutoRunMode returns the normalized auto_run mode, defaulting to "off".
func (h HarnessConfig) AutoRunMode() string {
	switch strings.ToLower(strings.TrimSpace(h.AutoRun)) {
	case "suggest":
		return "suggest"
	case "on":
		return "on"
	case "strict":
		return "strict"
	default:
		return "off"
	}
}

// A2AAuthConfig configures which authentication mechanisms the A2A server accepts.
// Multiple schemes can be enabled simultaneously — clients choose any one.
type A2AAuthConfig struct {
	// APIKey is the simplest shared-secret auth. All clients use the same key.
	// Empty = no API key auth.
	APIKey string `yaml:"api_key,omitempty"`

	// APIKeys allows multiple API keys for different clients/teams.
	// Both api_key and api_keys are merged; any match authenticates.
	APIKeys []string `yaml:"api_keys,omitempty"`

	// OAuth2 + PKCE / Device Flow for human-interactive agents.
	OAuth2 *A2AOAuth2Config `yaml:"oauth2,omitempty"`

	// OpenID Connect — layered on top of OAuth2, provides identity tokens.
	OIDC *A2AOIDCConfig `yaml:"oidc,omitempty"`

	// Mutual TLS for machine-to-machine. No secrets needed.
	MTLS *A2AMTLSConfig `yaml:"mtls,omitempty"`

	// HMACSecret is the shared secret for HS256/HS384/HS512 JWT signing.
	// MUST NOT be the clientID (which is public). Set this only if your IdP
	// uses HMAC-signed JWTs; most providers use RS256/ES256 instead.
	// Supports ${ENV_VAR} expansion.
	HMACSecret string `yaml:"hmac_secret,omitempty"`

	// ValidIssuers lists additional issuer URLs to accept in JWT validation.
	// The configured issuer_url is always allowed. Use this when your IdP
	// returns different issuer URLs in different contexts.
	ValidIssuers []string `yaml:"valid_issuers,omitempty"`
}

// A2AOAuth2Config for OAuth2 Authorization Code + PKCE or Device Flow.
// Use "provider" for built-in presets or set fields manually for custom IdP.
type A2AOAuth2Config struct {
	// Provider selects a built-in preset: "github", "google", "auth0", "azure".
	// When set, endpoint URLs and default_client_id are auto-populated.
	Provider string `yaml:"provider,omitempty"`

	// ClientID is the OAuth2 client ID. Auto-filled from provider preset.
	// Override to use your own registered OAuth App.
	ClientID string `yaml:"client_id,omitempty"`

	// ClientSecret is required for GitHub (confidential client).
	// Most other providers support PKCE without a secret.
	// Can also be set via GGCODE_OAUTH_CLIENT_SECRET env var.
	ClientSecret string `yaml:"client_secret,omitempty"`

	// IssuerURL is the OAuth2 issuer base URL. Auto-filled from provider preset.
	// For custom providers: "https://your-idp.example.com"
	IssuerURL string `yaml:"issuer_url,omitempty"`

	// Scopes are space-separated OAuth2 scopes. Auto-filled from provider preset.
	Scopes string `yaml:"scopes,omitempty"`

	// Flow selects the OAuth2 flow: "auto" (default), "pkce", or "device".
	// "auto" picks PKCE for desktop environments, Device Flow for headless.
	Flow string `yaml:"flow,omitempty"`
}

// A2AOIDCConfig adds OpenID Connect on top of OAuth2.
// Same provider preset and flow selection as OAuth2.
type A2AOIDCConfig struct {
	Provider     string `yaml:"provider,omitempty"`
	ClientID     string `yaml:"client_id,omitempty"`
	ClientSecret string `yaml:"client_secret,omitempty"`
	IssuerURL    string `yaml:"issuer_url,omitempty"` // must have /.well-known/openid-configuration
	Scopes       string `yaml:"scopes,omitempty"`     // should include "openid"
	Flow         string `yaml:"flow,omitempty"`       // "auto", "pkce", "device"
}

// A2AMTLSConfig for mutual TLS authentication.
type A2AMTLSConfig struct {
	CertFile string `yaml:"cert_file"` // server certificate
	KeyFile  string `yaml:"key_file"`  // server private key
	CAFile   string `yaml:"ca_file"`   // CA to verify client certs
}

func defaultEndpoint(displayName, protocol, baseURL, defaultModel string, tags ...string) EndpointConfig {
	ep := EndpointConfig{
		DisplayName:   displayName,
		Protocol:      protocol,
		BaseURL:       baseURL,
		AuthType:      "api_key",
		ContextWindow: inferContextWindow(defaultModel, protocol),
		MaxTokens:     inferMaxOutputTokens(defaultModel, protocol),
		DefaultModel:  defaultModel,
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
					"coding", "cn",
				),
				"cn-coding-anthropic": defaultEndpoint(
					"CN Coding Plan (Anthropic)",
					"anthropic",
					"https://open.bigmodel.cn/api/anthropic",
					"glm-5-turbo",
					"coding", "cn", "anthropic",
				),
				"global-coding-openai": defaultEndpoint(
					"Global Coding Plan",
					"openai",
					"https://api.z.ai/api/coding/paas/v4",
					"glm-5-turbo",
					"coding", "global",
				),
				"global-coding-anthropic": defaultEndpoint(
					"Global Coding Plan (Anthropic)",
					"anthropic",
					"https://api.z.ai/api/anthropic",
					"glm-5-turbo",
					"coding", "global", "anthropic",
				),
				"cn-api-openai": defaultEndpoint(
					"CN Standard API",
					"openai",
					"https://open.bigmodel.cn/api/paas/v4",
					"glm-4.5-air",
					"api", "cn",
				),
				"global-api-openai": defaultEndpoint(
					"Global Standard API",
					"openai",
					"https://api.z.ai/api/paas/v4",
					"glm-4.5-air",
					"api", "global",
				),
			}),
			"anthropic": defaultVendor("Anthropic", "${ANTHROPIC_API_KEY}", map[string]EndpointConfig{
				"api": defaultEndpoint(
					"Anthropic API",
					"anthropic",
					"https://api.anthropic.com",
					"claude-3-5-sonnet-latest",
					"official", "anthropic",
				),
				"oauth": func() EndpointConfig {
					ep := defaultEndpoint(
						"Anthropic OAuth",
						"anthropic",
						"https://api.anthropic.com",
						"claude-sonnet-4-20250514",
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
					"official", "openai",
				),
			}),
			"xiaomi-mimo": defaultVendor("XiaoMi MIMO", "${XIAOMI_MIMO_API_KEY}", map[string]EndpointConfig{
				"cn-openai": func() EndpointConfig {
					ep := defaultEndpoint(
						"XiaoMi MIMO API",
						"openai",
						"https://token-plan-cn.xiaomimimo.com/v1",
						"MiMo-V2.5-Pro",
						"api", "cn",
					)
					ep.Models = append([]string(nil), lookupVendorModels("xiaomi-mimo")...)
					return ep
				}(),
				"cn-anthropic": func() EndpointConfig {
					ep := defaultEndpoint(
						"XiaoMi MIMO API (Anthropic)",
						"anthropic",
						"https://token-plan-cn.xiaomimimo.com/anthropic",
						"MiMo-V2.5-Pro",
						"api", "cn", "anthropic",
					)
					ep.Models = append([]string(nil), lookupVendorModels("xiaomi-mimo")...)
					return ep
				}(),
			}),
			"google": defaultVendor("Google Gemini", "${GEMINI_API_KEY}", map[string]EndpointConfig{
				"api": defaultEndpoint(
					"Gemini API",
					"gemini",
					"https://generativelanguage.googleapis.com",
					"gemini-1.5-flash",
					"official", "gemini",
				),
			}),
			"groq": defaultVendor("Groq", "${GROQ_API_KEY}", map[string]EndpointConfig{
				"api": defaultEndpoint(
					"Groq API",
					"openai",
					"https://api.groq.com/openai/v1",
					"llama-3.1-8b-instant",
					"official", "openai-compatible", "fast",
				),
			}),
			"mistral": defaultVendor("Mistral", "${MISTRAL_API_KEY}", map[string]EndpointConfig{
				"api": defaultEndpoint(
					"Mistral API",
					"openai",
					"https://api.mistral.ai/v1",
					"mistral-small-latest",
					"official", "openai-compatible",
				),
			}),
			"deepseek": defaultVendor("DeepSeek", "${DEEPSEEK_API_KEY}", map[string]EndpointConfig{
				"api": defaultEndpoint(
					"DeepSeek API",
					"openai",
					"https://api.deepseek.com/v1",
					"deepseek-chat",
					"official", "openai-compatible", "reasoning",
				),
			}),
			"moonshot": defaultVendor("Moonshot AI", "${MOONSHOT_API_KEY}", map[string]EndpointConfig{
				"api": defaultEndpoint(
					"Moonshot API",
					"openai",
					"https://api.moonshot.cn/v1",
					"moonshot-v1-8k",
					"official", "openai-compatible", "cn",
				),
			}),
			"aliyun": defaultVendor("Aliyun Bailian Coding Plan", "${DASHSCOPE_API_KEY}", map[string]EndpointConfig{
				"coding-openai": defaultEndpoint(
					"Aliyun Bailian Coding Plan",
					"openai",
					"https://coding.dashscope.aliyuncs.com/v1",
					"qwen3-coder-plus",
					"official", "coding", "cn", "openai-compatible",
				),
				"coding-anthropic": defaultEndpoint(
					"Aliyun Bailian Coding Plan (Anthropic)",
					"anthropic",
					"https://coding.dashscope.aliyuncs.com/apps/anthropic",
					"qwen3-coder-plus",
					"official", "coding", "cn", "anthropic",
				),
			}),
			"kimi": defaultVendor("Kimi Coding Plan", "${KIMI_API_KEY}", map[string]EndpointConfig{
				"coding-openai": defaultEndpoint(
					"Kimi Coding Plan",
					"openai",
					"https://api.kimi.com/coding/v1",
					"kimi-for-coding",
					"official", "coding", "openai-compatible",
				),
				"coding-anthropic": defaultEndpoint(
					"Kimi Coding Plan (Anthropic)",
					"anthropic",
					"https://api.kimi.com/coding/",
					"kimi-for-coding",
					"official", "coding", "anthropic",
				),
			}),
			"minimax": defaultVendor("MiniMax Token Plan", "${MINIMAX_API_KEY}", map[string]EndpointConfig{
				"token-plan-openai": defaultEndpoint(
					"MiniMax Token Plan",
					"openai",
					"https://api.minimaxi.com/v1",
					"MiniMax-M2.7",
					"official", "coding", "openai-compatible",
				),
				"token-plan-anthropic": defaultEndpoint(
					"MiniMax Token Plan (Anthropic)",
					"anthropic",
					"https://api.minimaxi.com/anthropic",
					"MiniMax-M2.7",
					"official", "coding", "anthropic",
				),
				"global-openai": defaultEndpoint(
					"MiniMax Global",
					"openai",
					"https://api.minimax.io/v1",
					"MiniMax-M2.7",
					"official", "coding", "openai-compatible", "global",
				),
				"global-anthropic": defaultEndpoint(
					"MiniMax Global (Anthropic)",
					"anthropic",
					"https://api.minimax.io/anthropic",
					"MiniMax-M2.7",
					"official", "coding", "anthropic", "global",
				),
			}),
			"ark": defaultVendor("Volcengine Ark Coding Plan", "${ARK_API_KEY}", map[string]EndpointConfig{
				"coding-openai": defaultEndpoint(
					"Ark Coding Plan",
					"openai",
					"https://ark.cn-beijing.volces.com/api/coding/v3",
					"ark-code-latest",
					"official", "coding", "cn", "openai-compatible",
				),
				"coding-anthropic": defaultEndpoint(
					"Ark Coding Plan (Anthropic)",
					"anthropic",
					"https://ark.cn-beijing.volces.com/api/coding",
					"ark-code-latest",
					"official", "coding", "cn", "anthropic",
				),
			}),
			"github-copilot": defaultVendor("GitHub Copilot", "", map[string]EndpointConfig{
				"github.com": func() EndpointConfig {
					ep := defaultEndpoint(
						"GitHub.com",
						"copilot",
						auth.CopilotAPIBaseURL(""),
						"gpt-4o",
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
						"official", "oauth", "copilot", "enterprise",
					)
					ep.AuthType = "oauth"
					return ep
				}(),
			}),
			"ai-gateway": defaultVendor("AI Gateway", "", map[string]EndpointConfig{
				"aihubmix": defaultEndpoint(
					"AIHubMix",
					"openai",
					"https://aihubmix.com/v1",
					"",
					"gateway",
				),
				"getgoapi": defaultEndpoint(
					"GetGoAPI",
					"openai",
					"https://api.getgoapi.com/v1",
					"",
					"gateway",
				),
				"novita": defaultEndpoint(
					"Novita AI",
					"openai",
					"https://api.novita.ai/openai/v1",
					"",
					"gateway",
				),
				"nvidia": defaultEndpoint(
					"NVIDIA NIM",
					"openai",
					"https://integrate.api.nvidia.com/v1",
					"",
					"gateway",
				),
				"openrouter": defaultEndpoint(
					"OpenRouter",
					"openai",
					"https://openrouter.ai/api/v1",
					"",
					"gateway",
				),
				"poe": defaultEndpoint(
					"Poe",
					"openai",
					"https://api.poe.com/v1",
					"",
					"gateway",
				),
				"requesty": defaultEndpoint(
					"Requesty",
					"openai",
					"https://router.requesty.ai/v1",
					"",
					"gateway",
				),
				"together": defaultEndpoint(
					"Together AI",
					"openai",
					"https://api.together.xyz/v1",
					"",
					"gateway",
				),
				"perplexity": defaultEndpoint(
					"Perplexity",
					"openai",
					"https://api.perplexity.ai",
					"",
					"gateway",
				),
				"vercel": defaultEndpoint(
					"Vercel AI Gateway",
					"openai",
					"https://ai-gateway.vercel.sh/v1",
					"",
					"gateway",
				),
			}),
		},
	}
	cfg.expandEnv()
	populateDefaultModels(cfg)
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
	migrateLegacyA2AAPIKey(raw)
	if shouldApplyFirstLaunchAnthropicBootstrap(raw) {
		applyFirstLaunchAnthropicBootstrap(cfg)
	}

	// Auto-migrate plaintext API keys to environment variable references.
	// This sets os.Setenv for the current process and writes ~/.ggcode/keys.env
	// for future sessions, then rewrites the YAML to use ${VAR} references.
	migrated, migrateErr := MigratePlaintextAPIKeys(path)
	if migrateErr != nil {
		debug.Log("config", "MigratePlaintextAPIKeys error: %v", migrateErr)
	}
	if len(migrated) > 0 {
		for _, m := range migrated {
			switch m.Section {
			case "vendor":
				if m.Endpoint != "" {
				} else {
				}
			case "im", "mcp_env", "mcp_headers":
			default:
			}
		}
		// Reload the config file after migration rewrote it.
		data, err = os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("re-reading migrated config %s: %w", path, err)
		}
		raw = map[string]interface{}{}
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("parsing migrated config %s: %w", path, err)
		}
	}

	// Expand env vars
	lookup = runtimeEnvLookup(raw)

	// Remove deprecated system_prompt key from YAML if present.
	if _, has := raw["system_prompt"]; has {
		delete(raw, "system_prompt")
		if rewriteErr := rewriteYAML(path, raw); rewriteErr != nil {
			debug.Log("config", "failed to rewrite config after removing system_prompt: %v", rewriteErr)
		}
	}

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

	// Re-save to apply compact format (strip default vendors, inline models/tags).
	// Idempotent: compact files stay compact.
	cfg.globalSnap = nil
	cfg.instanceFields = nil
	if saveErr := cfg.Save(); saveErr != nil {
		return nil, fmt.Errorf("compact migration save: %w", saveErr)
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
	c.ExtraPrompt = ExpandEnvWithLookup(c.ExtraPrompt, lookup)
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
	// IM adapter env expansion: Extra and Env maps.
	for adapterName, adapter := range c.IM.Adapters {
		for key, val := range adapter.Extra {
			if s, ok := val.(string); ok {
				adapter.Extra[key] = ExpandEnvWithLookup(s, lookup)
			}
		}
		for key, val := range adapter.Env {
			adapter.Env[key] = ExpandEnvWithLookup(val, lookup)
		}
		c.IM.Adapters[adapterName] = adapter
	}
	// IM STT env expansion.
	c.IM.STT.APIKey = ExpandEnvWithLookup(c.IM.STT.APIKey, lookup)
	c.IM.STT.BaseURL = ExpandEnvWithLookup(c.IM.STT.BaseURL, lookup)
	c.IM.STT.Model = ExpandEnvWithLookup(c.IM.STT.Model, lookup)
	// A2A env expansion.

	for i, k := range c.A2A.Auth.APIKeys {
		c.A2A.Auth.APIKeys[i] = ExpandEnvWithLookup(k, lookup)
	}
	c.A2A.Host = ExpandEnvWithLookup(c.A2A.Host, lookup)
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
	// A2A defaults and validation.
	if !c.A2A.Disabled {
		if c.A2A.Port < 0 {
			return fmt.Errorf("a2a.port must not be negative")
		}
		if c.A2A.MaxTasks == 0 {
			c.A2A.MaxTasks = 5
		}
		if c.A2A.TaskTimeout == "" {
			c.A2A.TaskTimeout = "5m"
		}
		// Host defaults to 0.0.0.0 (LAN accessible) since A2A + mDNS is always on.
		// Loopback addresses are overridden to 0.0.0.0 so mDNS discovery and
		// LAN Chat work correctly.
		if c.A2A.Host == "" || isLoopbackHost(c.A2A.Host) {
			c.A2A.Host = "0.0.0.0"
		}
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

	// Validate hooks configuration.
	if hookErrs := hooks.ValidateHooks(c.Hooks); len(hookErrs) > 0 {
		return fmt.Errorf("invalid hooks config: %s", strings.Join(hookErrs, "; "))
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

// migrateLegacyA2AAPIKey silently moves a2a.api_key to a2a.auth.api_key
// in the raw YAML map if the legacy field is present and the new field
// is not already set. This is a backward-compatibility shim so existing
// configs with the old format keep working without user intervention.
func migrateLegacyA2AAPIKey(raw map[string]interface{}) {
	if raw == nil {
		return
	}
	a2a, ok := raw["a2a"].(map[string]interface{})
	if !ok {
		return
	}
	legacyKey, hasLegacy := a2a["api_key"]
	if !hasLegacy {
		return
	}
	auth, _ := a2a["auth"].(map[string]interface{})
	if auth == nil {
		auth = map[string]interface{}{}
	}
	if _, exists := auth["api_key"]; !exists {
		auth["api_key"] = legacyKey
		a2a["auth"] = auth
	}
	delete(a2a, "api_key")
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

func boolPtr(v bool) *bool {
	return &v
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
// BuildSystemPrompt builds the full system prompt by prepending the built-in
// default, appending the user's extra_prompt (if any), then runtime context.
func BuildSystemPrompt(extraPrompt, workingDir, language string, toolNames []string, gitStatus string, customCmds []string) string {
	toolNames = append([]string(nil), toolNames...)
	sort.Strings(toolNames)
	customCmds = append([]string(nil), customCmds...)
	sort.Strings(customCmds)

	var sb strings.Builder
	sb.WriteString(DefaultSystemPrompt)

	if extraPrompt != "" {
		sb.WriteString("\n\n## Extra instructions\n")
		sb.WriteString(extraPrompt)
	}

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
			cfgEP, exists := cfgVC.Endpoints[epName]
			if !exists {
				// Endpoint missing from config — add the built-in default.
				cfgVC.Endpoints[epName] = defaultEP
			} else if isPlaceholderBaseURL(cfgEP.BaseURL) {
				// Endpoint exists but has a placeholder base_url — patch it
				// with the correct built-in URL while preserving user overrides
				// for other fields (models, tags, selected_model, etc.).
				cfgEP.BaseURL = defaultEP.BaseURL
				if cfgEP.Protocol == "" {
					cfgEP.Protocol = defaultEP.Protocol
				}
				if cfgEP.DefaultModel == "" {
					cfgEP.DefaultModel = defaultEP.DefaultModel
				}
				cfgVC.Endpoints[epName] = cfgEP
			}
		}
		cfg.Vendors[vendorName] = cfgVC
	}

	// Migrate standalone gateway vendors into ai-gateway endpoints.
	gatewayVendors := map[string]string{
		"openrouter": "openrouter",
		"aihubmix":   "aihubmix",
		"getgoapi":   "getgoapi",
		"novita":     "novita",
		"poe":        "poe",
		"requesty":   "requesty",
		"vercel":     "vercel",
		"nvidia":     "nvidia",
		"together":   "together",
		"perplexity": "perplexity",
	}
	if agVC, ok := cfg.Vendors["ai-gateway"]; ok {
		for oldVendor, epName := range gatewayVendors {
			if oldVC, exists := cfg.Vendors[oldVendor]; exists {
				if _, hasEP := agVC.Endpoints[epName]; !hasEP {
					for _, ep := range oldVC.Endpoints {
						agVC.Endpoints[epName] = ep
					}
				}
				delete(cfg.Vendors, oldVendor)
			}
		}
		cfg.Vendors["ai-gateway"] = agVC
	}
}

// isPlaceholderBaseURL returns true if the URL looks like a template placeholder
// rather than a real endpoint (e.g. "https://your-global-api-endpoint.example.com/v1").
// We look for the "your-" prefix pattern which is used in all our placeholder URLs,
// so that legitimate example.com domains (used by some proxies) are not clobbered.
func isPlaceholderBaseURL(u string) bool {
	return strings.Contains(u, "your-") && strings.Contains(u, "example.com")
}

// rewriteYAML rewrites the config file from the given raw map.
func rewriteYAML(path string, raw map[string]interface{}) error {
	data, err := yaml.Marshal(raw)
	if err != nil {
		return err
	}
	return writeSecureConfigFile(path, data)
}
