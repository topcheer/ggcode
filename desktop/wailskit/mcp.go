package wailskit

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/topcheer/ggcode/internal/util"
	"os"
	"path/filepath"
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
// ggcode.yaml, MCP servers live in that workspace's mcp_servers.yaml - the
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
	// independently - a switchWorkspace in between spliced OLD workspace
	// yaml config with NEW workspace connection state.
	globalMu.RLock()
	chatSnap := activeChatBridge
	globalMu.RUnlock()

	cfg, err := loadSessionScopedConfigFor(chatSnap)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	// #606: list the same merged server set the runtime actually runs. The
	// runtime (BuildInteractiveRuntimeCore -> mcp.MergeStartupServers) folds
	// Claude migration files (.mcp.json / ~/.claude.json) into the yaml set,
	// so a yaml-only view here forked from reality: migrated servers were
	// live (tools callable) yet invisible in the MCP panel.
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
	merged, _ := mcp.MergeStartupServersWithDeleted(workDir, cfg.MCPServers, cfg.DeletedMCPServers)
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
	// #1434-B: args had no clear-channel twin of #979's env_clear/headers_clear
	// sentinels - an emptied args form produced nil Args, which the patch
	// layer reads as 'not provided' and the OLD args silently resurrected.
	// args_clear=1 (or an explicitly-empty args value) writes a non-nil
	// empty slice: the patch layer's 'cleared' semantic.
	if isTruthyFormFlag(values["args_clear"]) || values["args"] == "" {
		serverCfg.Args = []string{}
	} else if argsStr := values["args"]; argsStr != "" {
		args, err := parseShellArgs(argsStr)
		if err != nil {
			return fmt.Errorf("invalid args: %w", err)
		}
		serverCfg.Args = args
	}

	// Parse env_ prefixed keys into env map.
	// #979: an explicitly-cleared env must survive the len guard below: the
	// form sends env_clear=1 when the user deleted every env row, and the
	// patch layer (patchMCPServerConfig) defines "empty non-nil map =
	// cleared". Without the sentinel, a form with zero env_* keys produced a
	// nil Env ("not provided" -> keep old value), so stale credentials
	// silently resurrected on save.
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
		serverCfg.Env = map[string]string{} // non-nil empty: explicit clear
	}

	// Parse headers_ prefixed keys into headers map (same #979 semantics).
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
		serverCfg.Headers = map[string]string{} // non-nil empty: explicit clear
	}

	cfg.UpsertMCPServer(serverCfg)
	// SaveMCPServers writes only the scope's mcp_servers.yaml. A full cfg.Save()
	// would also rewrite the main config file for what is an MCP-only change.
	if err := cfg.SaveMCPServers(); err != nil {
		return err
	}
	// #498/#979: propagate immediately with the merge-equivalent set - for
	// workspace-scoped sessions the global-file watcher never sees this
	// write, and a yaml-only list would kick .mcp.json servers out of the
	// running session (Reload is replace-semantics).
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
	// #458: snapshot the bridge once so the scope decision and the Disconnect
	// below see the same session even if a workspace switch interleaves.
	// Without this, a switchWorkspace between loadSessionScopedConfig and the
	// Disconnect below can leave the removed server connected in the old workspace
	// while Disconnect operates on the new workspace's manager (#563).
	globalMu.RLock()
	chatSnap := activeChatBridge
	globalMu.RUnlock()
	cfg, err := loadSessionScopedConfigFor(chatSnap)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	removedYaml := cfg.RemoveMCPServer(name)
	if removedYaml {
		if err := cfg.SaveMCPServers(); err != nil {
			return err
		}
	}
	// #606: clean the migration-file side as well. This covers merged-only
	// servers (yaml removal returned false -> old code errored "not found")
	// and dual-side names (yaml removal alone would be resurrected by merge).
	removedOrigin, err := removeMigratedMCPServer(chatSnap, name)
	if err != nil {
		return err
	}
	if !removedYaml && !removedOrigin {
		return fmt.Errorf("MCP server %q not found", name)
	}
	// Tombstone the name: external apps rewrite their Claude registrations
	// behind our back; without the tombstone the merge re-imports the name on
	// the next panel read and it "comes back" as an unconfigured row.
	// removeMigratedMCPServer cleaned the origin files, so this is the only
	// remaining resurrect path. Re-adding via UpsertMCPServer clears it.
	cfg.RecordMCPDeleted(name)
	// Symmetric with SetMCPServerEnabled(false): disconnect immediately
	// instead of waiting for the ~2s hot-reload poll (which may also miss
	// workspace-scoped yaml changes) - without this the removed server's
	// tools stayed callable during the window (#408). Uses chatSnap to
	// ensure Disconnect targets the same session whose config we just saved.
	// The reload then pushes the merge-equivalent set (yaml ∪ .mcp.json);
	// since every origin of the name has been removed from disk, the merge
	// cannot resurrect it (#606), while other migrated servers survive (#979).
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
		// Not a parseable Claude config; leave untouched.
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
	// #1434-A: this overwrites ~/.claude.json - Claude Code's own config,
	// which carries project history/consent state and can reach MBs.
	// os.WriteFile is O_TRUNC: a mid-write crash truncates it, and on a
	// full disk/quota the open already truncated it even when the write
	// errors out cleanly - no backup, irreversible corruption. The
	// project's own standard (mcp_disabled.go, post-#781) is
	// util.AtomicWriteFile; a third-party file deserves no less.
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
	var inQuote byte  // 0 = no quote, '"' or '\'' = inside quote
	var escape bool   // true if next char is escaped
	seenChar := false // any char (incl. quotes) seen since last separator

	for i := 0; i < len(s); i++ {
		ch := s[i]

		if escape {
			// Escaped character - add it literally and clear escape flag
			current.WriteByte(ch)
			escape = false
			seenChar = true
			continue
		}

		if ch == '\\' {
			// Backslash escapes quotes only. A backslash before any other
			// character (or at end of input) is literal, so Windows paths
			// and glob patterns are not silently corrupted (#584 M1).
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
			// Explicit empty args ("") are meaningful CLI values - the old
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

	// Check for unbalanced quotes or pending escape
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
