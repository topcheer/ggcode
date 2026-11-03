package wailskit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/auth"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/util"
)

// mcpConfigWriteMu serializes full-file MCP config rewrites (#1813 case 1).
// See the comment on AddMCPServer.
var mcpConfigWriteMu sync.Mutex

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

	return LoadConfigForWorkspace(workDir)
}

// ListMCPServers returns all configured MCP servers for the active session's
// scope (workspace config when bound to a workspace, global otherwise).
func ListMCPServers() ([]MCPServerInfo, error) {

	chatSnap := GetChatBridge()

	cfg, err := loadSessionScopedConfigFor(chatSnap)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	servers := effectiveSessionServers(chatSnap, cfg)
	if len(servers) == 0 {
		return nil, nil
	}

	result := make([]MCPServerInfo, 0, len(servers))
	for _, s := range servers {
		result = append(result, MCPServerInfo{
			Name:     s.Name,
			Type:     s.Type,
			Command:  s.Command,
			Args:     s.Args,
			Env:      s.Env,
			URL:      s.URL,
			Headers:  s.Headers,
			Status:   "unknown",
			Disabled: plugin.MCPDisabledIn(chatScope(chatSnap), s.Name),
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
	chat := GetChatBridge()
	scope := chatScope(chat)
	if chat == nil || chat.mcpManager == nil {

		if err := plugin.SetMCPDisabledIn(scope, name, disabled); err != nil {
			return false
		}
		return true
	}

	if err := plugin.SetMCPDisabledIn(scope, name, disabled); err != nil {
		return false
	}
	if disabled {

		if !chat.mcpManager.Disconnect(name) {
			debug.Log("wailskit", "SetMCPServerEnabled: %s disabled on disk but not live in manager (not connected or unknown)", name)
		}
		return true
	}

	if chat.mcpManager.Reconnect(name) {
		return true
	}

	cfg, cfgErr := loadSessionScopedConfigFor(chat)
	if cfgErr != nil {

		debug.Log("wailskit", "SetMCPServerEnabled: %s enabled on disk but session config reload failed: %v", name, cfgErr)
		return true
	}
	reloadSessionMCPServers(chat, cfg)
	debug.Log("wailskit", "SetMCPServerEnabled: %s enabled on disk; rebuilt session MCP set (server was not live in manager)", name)
	return true
}

// chatScope returns the workspace scope for MCP disabled-state operations
// (#2390): the active chat session's working directory, or "" (the global
// bucket) when no session is bound. Nil-safe.
func chatScope(chat *ChatBridge) string {
	if chat == nil {
		return ""
	}
	return chat.WorkingDir()
}

func ReconnectMCPServer(name string) bool {
	chat := GetChatBridge()
	if chat == nil || chat.mcpManager == nil {
		return false
	}
	return chat.mcpManager.Reconnect(name)
}

// ForceReauthMCPServer deletes the server-name-specific OAuth credential and
// triggers a reconnect. The next request will start a fresh OAuth flow.
func ForceReauthMCPServer(name string) bool {
	chat := GetChatBridge()
	if chat == nil || chat.mcpManager == nil {
		return false
	}
	return chat.mcpManager.ForceReauth(name)
}

// reloadSessionMCPServers pushes the effective server list for the session
// into the session's MCP manager. The hot-reload watcher only polls the GLOBAL
// mcp_servers.yaml (interactive_core.go: NewMCPHotReload(config.ConfigDir())),
// so workspace-scoped writes from AddMCPServer are invisible to it (#498).
// Without this explicit reload, an edited or newly added workspace server
// never takes effect in the running session - and even the UI's Reconnect
// button cannot fix it, because MCPPlugin.Connect short-circuits on the
// cached adapter while the plugin's own cfg is stale. Reload rebuilds
// changed/new plugins from the fresh list.
//
// #979: the list pushed must be the SAME merged set the runtime runs at
// startup (BuildInteractiveRuntimeCore -> mcp.MergeStartupServers: yaml ∪
// project .mcp.json ∪ ~/.claude.json). MCPManager.Reload has replace
// semantics, so passing the yaml-only cfg.MCPServers silently disconnected
// every Claude-migrated server from the running session on ANY Add/Remove.
// The merge is in-memory only (merge never persists to the yaml), which is
// safe for Remove because the caller has already deleted the name from
// every origin file (#606) - the merge cannot resurrect what no file
// provides anymore.
func reloadSessionMCPServers(chat *ChatBridge, cfg *config.Config) {
	if chat == nil || chat.mcpManager == nil || cfg == nil {
		return
	}
	workDir := ""
	if chat != nil {
		workDir = chat.WorkingDir()
	}
	merged, mergeWarnings := mcp.MergeStartupServersWithDeleted(workDir, cfg.MCPServers, cfg.DeletedMCPServers)

	allowed, blocked, gateWarnings := mcp.GateProjectServers(merged, workDir)
	for _, warning := range append(mergeWarnings, gateWarnings...) {
		debug.Log("mcp", "%s", warning)
	}
	if len(blocked) > 0 || len(mergeWarnings) > 0 {
		blockedNames := make([]string, 0, len(blocked))
		for _, b := range blocked {
			blockedNames = append(blockedNames, b.Name)
		}
		payload := map[string]any{
			"blocked":  blockedNames,
			"warnings": mergeWarnings,
		}
		if raw, err := json.Marshal(payload); err == nil && chat.OnStreamEvent != nil {
			chat.OnStreamEvent("mcp:gate", raw)
		}
	}
	chat.mcpManager.Reload(context.Background(), allowed)
}

// AddMCPServer adds a new MCP server configuration.
// Channel defaults are heterogeneous BY CONTRACT (#2391): "args" is
// clear-on-missing (an absent or empty key writes a non-nil empty Args;
// the frontend relies on this to clear by omitting the key), while
// env_*/headers_* keys are preserve-on-missing (cleared only via the
// env_clear/headers_clear sentinels - #979/#1434-B).
// The values map may contain:
//   - "name" (required): server name
//   - "type": "stdio", "http", "ws" (default: "stdio")
//   - "command": executable command (for stdio type)
//   - "args": space-separated arguments (for stdio type)
//   - "url": server URL (for http/ws type)
//   - "headers_*": HTTP headers (keys like "headers_Authorization")
//   - "env_*": environment variables (keys like "env_KEY")
func AddMCPServer(values map[string]string) error {

	mcpConfigWriteMu.Lock()
	defer mcpConfigWriteMu.Unlock()

	chatSnap := GetChatBridge()
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
	// #4496: look the server up in the EFFECTIVE set (yaml ∪ Claude-migrated,
	// same MergeStartupServersWithDeleted union the runtime and the panel's
	// ListMCPServers use), not just cfg.MCPServers. Editing a migrated-only
	// http server with no type in the form used to find existing==nil,
	// default to stdio, and the yaml write + stdio validation then evicted
	// the working server from the runtime entirely (#249 covered yaml only).
	var existing *config.MCPServerConfig
	for _, ms := range effectiveSessionServers(chatSnap, cfg) {
		if ms.Name == name {
			m := ms
			existing = &m
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

	if isTruthyFormFlag(values["command_clear"]) || values["command"] == "" {
		serverCfg.CommandClear = true
	}
	if isTruthyFormFlag(values["url_clear"]) || values["url"] == "" {
		serverCfg.URLClear = true
	}

	if isTruthyFormFlag(values["args_clear"]) || values["args"] == "" {
		serverCfg.Args = []string{}
	} else if argsStr := values["args"]; argsStr != "" {
		args, err := parseShellArgs(argsStr)
		if err != nil {
			return fmt.Errorf("invalid args: %w", err)
		}
		serverCfg.Args = args
	}

	envClear := isTruthyFormFlag(values["env_clear"])
	env := make(map[string]string)
	for k, v := range values {
		if len(k) > 4 && k[:4] == "env_" && k != "env_clear" {
			env[k[4:]] = v
		}
	}
	if len(env) > 0 {
		serverCfg.Env = env
	} else if envClear {
		serverCfg.Env = map[string]string{}
	}

	headersClear := isTruthyFormFlag(values["headers_clear"])
	headers := make(map[string]string)
	for k, v := range values {
		if len(k) > 8 && k[:8] == "headers_" && k != "headers_clear" {
			headers[k[8:]] = v
		}
	}
	if len(headers) > 0 {
		serverCfg.Headers = headers
	} else if headersClear {
		serverCfg.Headers = map[string]string{}
	}

	cfg.UpsertMCPServer(serverCfg)

	if err := cfg.SaveMCPServers(); err != nil {
		return err
	}

	reloadSessionMCPServers(chatSnap, cfg)
	return nil
}

// RemoveMCPServer removes an MCP server by name from the active session's
// scope: the workspace mcp_servers.yaml when bound to a workspace (else
// global), AND - when the name is provided by a Claude migration file
// (.mcp.json / ~/.claude.json / ~/.claude/mcp.json) instead of or in addition
// to the yaml - that origin file too (#606). Without the origin cleanup the
// remove path had two dead-ends: merged-only servers failed with "not found"
// even though their tools were live, and removing a yaml copy was resurrected
// by the merge that reloadSessionMCPServers (and every startup) re-runs.
func RemoveMCPServer(name string) error {

	mcpConfigWriteMu.Lock()
	defer mcpConfigWriteMu.Unlock()

	chatSnap := GetChatBridge()
	cfg, err := loadSessionScopedConfigFor(chatSnap)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	removedYaml := cfg.RemoveMCPServer(name)
	if removedYaml {
		if err := cfg.SaveMCPServers(); err != nil {

			cfg.ClearMCPDeleted(name)
			return err
		}
	}

	removedOrigin, err := removeMigratedMCPServer(chatSnap, name)
	if err != nil {

		cfg.RecordMCPDeleted(name)

		if chatSnap != nil && chatSnap.mcpManager != nil {
			_ = chatSnap.mcpManager.Disconnect(name)
			reloadSessionMCPServers(chatSnap, cfg)
		}
		return err
	}
	if !removedYaml && !removedOrigin {
		return fmt.Errorf("MCP server %q not found", name)
	}

	cfg.RecordMCPDeleted(name)

	if h := mcp.NewOAuthHandler(name, "", auth.DefaultStore()); h != nil {
		if derr := h.DeleteServerToken(); derr != nil {
			debug.Log("wailskit.mcp", "remove %q: delete server token failed (will self-heal on 401): %v", name, derr)
		}
	}

	if chatSnap != nil && chatSnap.mcpManager != nil {
		_ = chatSnap.mcpManager.Disconnect(name)
		reloadSessionMCPServers(chatSnap, cfg)
	}
	return nil
}

// effectiveSessionServers returns the effective MCP server set for the
// session scope: the yaml servers plus Claude-migrated servers, computed by
// the exact same merge the runtime runs at startup (BuildInteractiveCore ->
// mcp.MergeStartupServers). The desktop MCP list must match the running set
// or migrated servers become invisible-but-active (#606).
func effectiveSessionServers(chat *ChatBridge, cfg *config.Config) []config.MCPServerConfig {
	if cfg == nil {
		return nil
	}
	workDir := ""
	if chat != nil {
		workDir = chat.WorkingDir()
	}
	merged, _ := mcp.MergeStartupServersWithDeleted(workDir, cfg.MCPServers, cfg.DeletedMCPServers)
	return merged
}

// removeMigratedMCPServer deletes the named server from every Claude
// migration file that provides it (.mcp.json / ~/.claude.json /
// ~/.claude/mcp.json - the same sources mcp.MergeStartupServers reads, kept
// in sync via claudeMigrationPaths). Other top-level keys of the file are
// preserved byte-exactly via json.RawMessage round-tripping. Returns true
// when at least one file was rewritten.
func removeMigratedMCPServer(chat *ChatBridge, name string) (bool, error) {
	workDir := ""
	if chat != nil {
		workDir = chat.WorkingDir()
	}
	removed := false
	for _, path := range claudeMigrationPaths(workDir) {
		changed, err := removeFromClaudeFile(path, name)
		if err != nil {
			return removed, err
		}
		removed = removed || changed
	}
	return removed, nil
}

// removeFromClaudeFile deletes the named server from one Claude config file.
// It reports false (and leaves the file untouched) when the file is missing,
// unparseable, or does not list the server. Other top-level keys are preserved
// byte-exactly via json.RawMessage round-tripping.
func removeFromClaudeFile(path, name string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(data, &parsed); err != nil {

		return false, nil
	}
	var servers map[string]json.RawMessage
	if raw, ok := parsed["mcpServers"]; ok {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return false, nil
		}
	}
	if _, ok := servers[name]; !ok {
		return false, nil
	}
	delete(servers, name)
	updated, err := json.Marshal(servers)
	if err != nil {
		return false, fmt.Errorf("encode %s: %w", path, err)
	}
	parsed["mcpServers"] = updated
	out, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return false, fmt.Errorf("encode %s: %w", path, err)
	}

	if err := util.AtomicWriteFile(path, append(out, '\n'), 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

// claudeMigrationPaths mirrors internal/mcp.knownClaudeSources: the project
// .mcp.json plus the user-level Claude files. Kept local because the
// internal/mcp helper is unexported; the merge (reader side) is the source
// of truth these paths must stay aligned with.
func claudeMigrationPaths(workDir string) []string {
	paths := []string{filepath.Join(workDir, ".mcp.json")}
	if home := config.HomeDir(); strings.TrimSpace(home) != "" {
		paths = append(paths,
			filepath.Join(home, ".claude.json"),
			filepath.Join(home, ".claude", "mcp.json"),
		)
	}
	return paths
}

// isTruthyFormFlag interprets a form-sent boolean-ish flag value. The desktop
// form protocol is map[string]string, so any of "1"/"true"/"yes"/"on"
// (case-insensitive) enables the flag; anything else (including missing)
// leaves it off.
func isTruthyFormFlag(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// parseShellArgs splits a command-line argument string with quote awareness.
// Unlike strings.Fields, it respects double and single quotes so arguments
// containing spaces (e.g. "/Users/John Doe/config.json") are kept intact.
// Returns an error if quotes are unbalanced (#584 M1).
func parseShellArgs(s string) ([]string, error) {
	var args []string
	var current strings.Builder
	var inQuote byte // 0 = no quote, '"' or '\'' = inside quote
	var escape bool  // true if next char is escaped
	seenChar := false

	for i := 0; i < len(s); i++ {
		ch := s[i]

		if escape {

			current.WriteByte(ch)
			escape = false
			seenChar = true
			continue
		}

		if ch == '\\' {

			if i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\'') {
				escape = true
				seenChar = true
				continue
			}
			current.WriteByte(ch)
			seenChar = true
			continue
		}

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

	if inQuote != 0 {
		return nil, fmt.Errorf("unbalanced quote: missing closing %q", string(inQuote))
	}
	if escape {
		return nil, fmt.Errorf("unterminated escape at end of string")
	}

	if seenChar {
		args = append(args, current.String())
	}

	return args, nil
}
