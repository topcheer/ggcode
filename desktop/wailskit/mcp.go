package wailskit

import (
	"context"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/plugin"
)

// MCPServerInfo is a frontend-friendly representation of an MCP server config.
type MCPServerInfo struct {
	Name          string            `json:"name"`
	Type          string            `json:"type,omitempty"`
	Command       string            `json:"command,omitempty"`
	Args          []string          `json:"args,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	URL           string            `json:"url,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	Status        string            `json:"status,omitempty"`
	Error         string            `json:"error,omitempty"`
	Disabled      bool              `json:"disabled,omitempty"`
	Connected     bool              `json:"connected,omitempty"`
	OAuthRequired bool              `json:"oauthRequired,omitempty"`
}

// loadSessionScopedConfig loads the config matching the active chat session's
// scope (#248). When a session is bound to a workspace that has its own
// ggcode.yaml, MCP servers live in that workspace's mcp_servers.yaml — the
// same file the session's mcpManager reads. Otherwise the global config is
// used. Without this, Add/Remove/List would touch the global file while the
// session runs workspace servers, so changes never took effect (and removals
// resurrected on reload).
func loadSessionScopedConfig() (*config.Config, error) {
	globalMu.RLock()
	chat := activeChatBridge
	globalMu.RUnlock()
	return loadSessionScopedConfigFor(chat)
}

// loadSessionScopedConfigFor is loadSessionScopedConfig with a pre-snapped
// bridge (#458), so config load and status lookup share one bridge view.
func loadSessionScopedConfigFor(chat *ChatBridge) (*config.Config, error) {
	workDir := ""
	if chat != nil {
		workDir = chat.WorkingDir()
	}
	if workDir == "" {
		return config.Load(config.ConfigPath())
	}
	// Same loader the session uses (config.go LoadConfigForWorkspace), so the
	// MCP list and save target match what the session's mcpManager reads.
	return LoadConfigForWorkspace(workDir)
}

// ListMCPServers returns all configured MCP servers for the active session's
// scope (workspace config when bound to a workspace, global otherwise).
func ListMCPServers() ([]MCPServerInfo, error) {
	// #458: snapshot the bridge ONCE and thread it through both the config
	// load and the status lookup. The old code read activeChatBridge twice
	// independently — a switchWorkspace in between spliced OLD workspace
	// yaml config with NEW workspace connection state.
	globalMu.RLock()
	chatSnap := activeChatBridge
	globalMu.RUnlock()

	cfg, err := loadSessionScopedConfigFor(chatSnap)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if len(cfg.MCPServers) == 0 {
		return nil, nil
	}

	result := make([]MCPServerInfo, 0, len(cfg.MCPServers))
	for _, s := range cfg.MCPServers {
		result = append(result, MCPServerInfo{
			Name:     s.Name,
			Type:     s.Type,
			Command:  s.Command,
			Args:     s.Args,
			Env:      s.Env,
			URL:      s.URL,
			Headers:  s.Headers,
			Status:   "unknown",
			Disabled: plugin.MCPDisabled(s.Name),
		})
	}
	chat := chatSnap
	if chat == nil || chat.mcpManager == nil {
		return result, nil
	}
	snapshot := chat.mcpManager.Snapshot()
	byName := make(map[string]plugin.MCPServerInfo, len(snapshot))
	for _, info := range snapshot {
		byName[info.Name] = info
	}
	for i := range result {
		if info, ok := byName[result[i].Name]; ok {
			result[i].Status = string(info.Status)
			result[i].Error = info.Error
			result[i].Disabled = info.Disabled
			result[i].Connected = info.Status == plugin.MCPStatusConnected
			result[i].OAuthRequired = info.OAuthRequired
		}
	}
	return result, nil
}

func SetMCPServerEnabled(name string, enabled bool) bool {
	disabled := !enabled
	globalMu.RLock()
	chat := activeChatBridge
	globalMu.RUnlock()
	if chat == nil || chat.mcpManager == nil {
		// Persist the toggle FIRST so the UI state matches disk (#408):
		// returning false after writing misled the frontend into showing
		// "failed" for a change that had already taken effect.
		plugin.SetMCPDisabled(name, disabled)
		return true
	}
	plugin.SetMCPDisabled(name, disabled)
	if disabled {
		return chat.mcpManager.Disconnect(name)
	}
	return chat.mcpManager.Reconnect(name)
}

func ReconnectMCPServer(name string) bool {
	globalMu.RLock()
	chat := activeChatBridge
	globalMu.RUnlock()
	if chat == nil || chat.mcpManager == nil {
		return false
	}
	return chat.mcpManager.Reconnect(name)
}

// ForceReauthMCPServer deletes the server-name-specific OAuth credential and
// triggers a reconnect. The next request will start a fresh OAuth flow.
func ForceReauthMCPServer(name string) bool {
	globalMu.RLock()
	chat := activeChatBridge
	globalMu.RUnlock()
	if chat == nil || chat.mcpManager == nil {
		return false
	}
	return chat.mcpManager.ForceReauth(name)
}

// reloadSessionMCPServers pushes a freshly-loaded server list into the
// session's MCP manager. The hot-reload watcher only polls the GLOBAL
// mcp_servers.yaml (interactive_core.go: NewMCPHotReload(config.ConfigDir())),
// so workspace-scoped writes from AddMCPServer are invisible to it (#498).
// Without this explicit reload, an edited or newly added workspace server
// never takes effect in the running session — and even the UI's Reconnect
// button cannot fix it, because MCPPlugin.Connect short-circuits on the
// cached adapter while the plugin's own cfg is stale. Reload rebuilds
// changed/new plugins from the fresh list, computing the same merged server
// set a watcher-triggered reload would (MergeStartupServers is idempotent
// for already-persisted Claude-migrated servers).
func reloadSessionMCPServers(chat *ChatBridge, servers []config.MCPServerConfig) {
	if chat == nil || chat.mcpManager == nil {
		return
	}
	merged, _ := mcp.MergeStartupServers(chat.WorkingDir(), servers)
	chat.mcpManager.Reload(context.Background(), merged)
}

// AddMCPServer adds a new MCP server configuration.
// The values map may contain:
//   - "name" (required): server name
//   - "type": "stdio", "http", "ws" (default: "stdio")
//   - "command": executable command (for stdio type)
//   - "args": space-separated arguments (for stdio type)
//   - "url": server URL (for http/ws type)
//   - "headers_*": HTTP headers (keys like "headers_Authorization")
//   - "env_*": environment variables (keys like "env_KEY")
func AddMCPServer(values map[string]string) error {
	// #458: snapshot the bridge once so the scope decision and the reload
	// below see the same session even if a workspace switch interleaves.
	globalMu.RLock()
	chatSnap := activeChatBridge
	globalMu.RUnlock()
	cfg, err := loadSessionScopedConfigFor(chatSnap)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	name := values["name"]
	if name == "" {
		return fmt.Errorf("name is required")
	}

	// Preserve the existing server's type when the form omits it (#249):
	// a form defaulting to "stdio" must not silently flip an http/ws server.
	// UpsertMCPServer patches only non-zero fields, so an explicit empty type
	// keeps the stored value; new servers still default to stdio.
	var existing *config.MCPServerConfig
	for i := range cfg.MCPServers {
		if cfg.MCPServers[i].Name == name {
			existing = &cfg.MCPServers[i]
			break
		}
	}

	serverType := values["type"]
	if serverType == "" {
		if existing != nil && existing.Type != "" {
			serverType = existing.Type
		} else {
			serverType = "stdio"
		}
	}

	serverCfg := config.MCPServerConfig{
		Name:    name,
		Type:    serverType,
		Command: values["command"],
		URL:     values["url"],
	}

	// Parse args from space-separated string with quote awareness.
	// strings.Fields splits quoted paths containing spaces (e.g. "/Users/John Doe/").
	// parseShellArgs handles " and ' quoting correctly.
	if argsStr := values["args"]; argsStr != "" {
		serverCfg.Args = parseShellArgs(argsStr)
	}

	// Parse env_ prefixed keys into env map
	env := make(map[string]string)
	for k, v := range values {
		if len(k) > 4 && k[:4] == "env_" {
			env[k[4:]] = v
		}
	}
	if len(env) > 0 {
		serverCfg.Env = env
	}

	// Parse headers_ prefixed keys into headers map
	headers := make(map[string]string)
	for k, v := range values {
		if len(k) > 8 && k[:8] == "headers_" {
			headers[k[8:]] = v
		}
	}
	if len(headers) > 0 {
		serverCfg.Headers = headers
	}

	cfg.UpsertMCPServer(serverCfg)
	// SaveMCPServers writes only the scope's mcp_servers.yaml. A full cfg.Save()
	// would also rewrite the main config file for what is an MCP-only change.
	if err := cfg.SaveMCPServers(); err != nil {
		return err
	}
	// #498: propagate immediately — for workspace-scoped sessions the global-
	// file watcher never sees this write, so without an explicit Reload the
	// running session keeps the old connection (or never learns about a new
	// server) until restart. Symmetric with RemoveMCPServer's #408 Disconnect.
	reloadSessionMCPServers(chatSnap, cfg.MCPServers)
	return nil
}

// RemoveMCPServer removes an MCP server by name from the active session's
// scope (workspace mcp_servers.yaml when bound to a workspace, else global).
func RemoveMCPServer(name string) error {
	cfg, err := loadSessionScopedConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if !cfg.RemoveMCPServer(name) {
		return fmt.Errorf("MCP server %q not found", name)
	}
	if err := cfg.SaveMCPServers(); err != nil {
		return err
	}
	// Symmetric with SetMCPServerEnabled(false): disconnect immediately
	// instead of waiting for the ~2s hot-reload poll (which may also miss
	// workspace-scoped yaml changes) — without this the removed server's
	// tools stayed callable during the window (#408).
	globalMu.RLock()
	chat := activeChatBridge
	globalMu.RUnlock()
	if chat != nil && chat.mcpManager != nil {
		_ = chat.mcpManager.Disconnect(name)
	}
	return nil
}

// parseShellArgs splits a command-line argument string with quote awareness.
// Unlike strings.Fields, it respects double and single quotes so arguments
// containing spaces (e.g. "/Users/John Doe/config.json") are kept intact.
func parseShellArgs(s string) []string {
	var args []string
	var current strings.Builder
	var inQuote byte  // 0 = no quote, '"' or '\'' = inside quote
	seenChar := false // any char (incl. quotes) seen since last separator

	for i := 0; i < len(s); i++ {
		ch := s[i]

		if inQuote != 0 {
			if ch == inQuote {
				inQuote = 0
			} else {
				current.WriteByte(ch)
			}
			seenChar = true
			continue
		}

		switch ch {
		case '"', '\'':
			inQuote = ch
			seenChar = true
		case ' ', '\t', '\n', '\r':
			// Explicit empty args ("") are meaningful CLI values — the old
			// current.Len()>0 guard dropped them, shifting every later arg
			// (#203).
			if seenChar {
				args = append(args, current.String())
				current.Reset()
				seenChar = false
			}
		default:
			current.WriteByte(ch)
			seenChar = true
		}
	}

	if seenChar {
		args = append(args, current.String())
	}

	return args
}
