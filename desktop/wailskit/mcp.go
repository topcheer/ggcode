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

var mcpConfigWriteMu sync.Mutex

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

func ForceReauthMCPServer(name string) bool {
	chat := GetChatBridge()
	if chat == nil || chat.mcpManager == nil {
		return false
	}
	return chat.mcpManager.ForceReauth(name)
}

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

func isTruthyFormFlag(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func parseShellArgs(s string) ([]string, error) {
	var args []string
	var current strings.Builder
	var inQuote byte
	var escape bool
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
