package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/config"
)

type claudeConfigFile struct {
	MCPServers map[string]config.MCPServerConfig `json:"mcpServers"`
}

type migrationSource struct {
	Path     string
	Source   string
	Priority int
}

func MergeStartupServers(workingDir string, explicit []config.MCPServerConfig) ([]config.MCPServerConfig, []string) {
	return mergeServers(explicit, knownClaudeSources(workingDir))
}

func PersistUserClaudeServers(cfg *config.Config) ([]string, bool, error) {
	if cfg == nil {
		return nil, false, fmt.Errorf("config is nil")
	}
	merged, warnings := mergeServers(cfg.MCPServers, knownUserClaudeSources())
	if !sameServerSet(cfg.MCPServers, merged) {
		cfg.MCPServers = merged
		if err := cfg.Save(); err != nil {
			return warnings, false, err
		}
		return append(warnings, fmt.Sprintf("info: migrated Claude MCP servers into %s", cfg.FilePath)), true, nil
	}
	return warnings, false, nil
}

func mergeServers(explicit []config.MCPServerConfig, sources []migrationSource) ([]config.MCPServerConfig, []string) {
	merged := make([]config.MCPServerConfig, 0, len(explicit))
	warnings := make([]string, 0)
	usedNames := make(map[string]string, len(explicit))
	usedSigs := make(map[string]string, len(explicit))

	for _, server := range explicit {
		cfg := server
		if strings.TrimSpace(cfg.Source) == "" {
			cfg.Source = "ggcode"
		}
		merged = append(merged, cfg)
		usedNames[cfg.Name] = cfg.Source
		if sig := serverSignature(cfg); sig != "" {
			usedSigs[sig] = cfg.Name
		}
	}

	for _, source := range sources {
		servers, err := loadClaudeServers(source)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("warning: failed to read %s MCP servers: %v", source.Source, err))
			continue
		}
		for _, server := range servers {
			if owner, exists := usedNames[server.Name]; exists {
				warnings = append(warnings, fmt.Sprintf("warning: skipped migrated MCP server %s from %s (name already provided by %s)", server.Name, source.Source, owner))
				continue
			}
			if sig := serverSignature(server); sig != "" {
				if owner, exists := usedSigs[sig]; exists {
					warnings = append(warnings, fmt.Sprintf("warning: skipped migrated MCP server %s from %s (duplicate of %s)", server.Name, source.Source, owner))
					continue
				}
				usedSigs[sig] = server.Name
			}
			usedNames[server.Name] = server.Source
			merged = append(merged, server)
		}
	}
	return merged, warnings
}

func knownClaudeSources(workingDir string) []migrationSource {
	sources := []migrationSource{
		{Path: filepath.Join(workingDir, ".mcp.json"), Source: "claude-project", Priority: 3},
	}
	return append(sources, knownUserClaudeSources()...)
}

func knownUserClaudeSources() []migrationSource {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil
	}
	return []migrationSource{
		{Path: filepath.Join(home, ".claude.json"), Source: "claude-user", Priority: 2},
		{Path: filepath.Join(home, ".claude", "mcp.json"), Source: "claude-user-legacy", Priority: 1},
	}
}

func loadClaudeServers(source migrationSource) ([]config.MCPServerConfig, error) {
	data, err := os.ReadFile(source.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var parsed claudeConfigFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	if len(parsed.MCPServers) == 0 {
		return nil, nil
	}
	servers := make([]config.MCPServerConfig, 0, len(parsed.MCPServers))
	for name, cfg := range parsed.MCPServers {
		cfg.Name = name
		cfg.Type = normalizedTransport(cfg.Type)
		cfg.Command = config.ExpandEnv(cfg.Command)
		cfg.URL = config.ExpandEnv(cfg.URL)
		for i, arg := range cfg.Args {
			cfg.Args[i] = config.ExpandEnv(arg)
		}
		for key, value := range cfg.Env {
			cfg.Env[key] = config.ExpandEnv(value)
		}
		for key, value := range cfg.Headers {
			cfg.Headers[key] = config.ExpandEnv(value)
		}
		cfg.Source = source.Source
		cfg.OriginPath = source.Path
		cfg.Migrated = true
		switch cfg.Type {
		case "stdio":
			if strings.TrimSpace(cfg.Command) == "" {
				continue
			}
		case "http":
			if strings.TrimSpace(cfg.URL) == "" {
				continue
			}
		default:
			continue
		}
		servers = append(servers, cfg)
	}
	return servers, nil
}

func normalizedTransport(transport string) string {
	normalized := strings.ToLower(strings.TrimSpace(transport))
	if normalized == "" {
		return "stdio"
	}
	return normalized
}

func serverSignature(cfg config.MCPServerConfig) string {
	switch normalizedTransport(cfg.Type) {
	case "http":
		return "http:" + strings.TrimSpace(cfg.URL)
	default:
		parts := append([]string{strings.TrimSpace(cfg.Command)}, cfg.Args...)
		data, _ := json.Marshal(parts)
		return "stdio:" + string(data)
	}
}

func sameServerSet(a, b []config.MCPServerConfig) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameServerConfig(a[i], b[i]) {
			return false
		}
	}
	return true
}

func sameServerConfig(a, b config.MCPServerConfig) bool {
	if strings.TrimSpace(a.Name) != strings.TrimSpace(b.Name) {
		return false
	}
	if normalizedTransport(a.Type) != normalizedTransport(b.Type) {
		return false
	}
	if strings.TrimSpace(a.Command) != strings.TrimSpace(b.Command) {
		return false
	}
	if strings.TrimSpace(a.URL) != strings.TrimSpace(b.URL) {
		return false
	}
	if len(a.Args) != len(b.Args) {
		return false
	}
	for i := range a.Args {
		if a.Args[i] != b.Args[i] {
			return false
		}
	}
	if !sameStringMap(a.Env, b.Env) || !sameStringMap(a.Headers, b.Headers) {
		return false
	}
	return true
}

func sameStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}
