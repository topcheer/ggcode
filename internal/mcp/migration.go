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
	return mergeServers(explicit, knownClaudeSources(workingDir), nil)
}

// MergeStartupServersWithDeleted is MergeStartupServers with deletion
// tombstones: names the user explicitly deleted are filtered out of BOTH
// the explicit list and the migrated sources, so a Claude source file
// rewritten behind our back (e.g. Pen.app re-adding its registration)
// cannot resurrect them.
func MergeStartupServersWithDeleted(workingDir string, explicit []config.MCPServerConfig, deleted []string) ([]config.MCPServerConfig, []string) {
	return mergeServers(explicit, knownClaudeSources(workingDir), deleted)
}

func PersistUserClaudeServers(cfg *config.Config) ([]string, bool, error) {
	if cfg == nil {
		return nil, false, fmt.Errorf("config is nil")
	}
	merged, warnings := mergeServers(cfg.MCPServers, knownUserClaudeSources(), cfg.DeletedMCPServers)
	if !sameServerSet(cfg.MCPServers, merged) {
		cfg.MCPServers = merged
		if err := cfg.Save(); err != nil {
			return warnings, false, err
		}
		return append(warnings, fmt.Sprintf("info: migrated Claude MCP servers into %s", cfg.FilePath)), true, nil
	}
	return warnings, false, nil
}

func mergeServers(explicit []config.MCPServerConfig, sources []migrationSource, deleted []string) ([]config.MCPServerConfig, []string) {
	deletedSet := make(map[string]bool, len(deleted))
	for _, name := range deleted {
		deletedSet[strings.TrimSpace(name)] = true
	}
	merged := make([]config.MCPServerConfig, 0, len(explicit))
	warnings := make([]string, 0)
	usedNames := make(map[string]string, len(explicit))
	usedSigs := make(map[string]string, len(explicit))

	for _, server := range explicit {
		// NOTE: no tombstone filter here, deliberately. The explicit list is
		// ggcode's own yaml: RemoveMCPServer already removed the name from it
		// at delete time, so a name appearing here again is an explicit
		// re-add (panel Add via UpsertMCPServer, which also clears the
		// tombstone, or a manual yaml edit) and must win over the tombstone.
		// Tombstones only guard the migration-source side below.
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
		servers, srcWarnings, err := loadClaudeServers(source)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("warning: failed to read %s MCP servers: %v", source.Source, err))
			continue
		}
		warnings = append(warnings, srcWarnings...)
		for _, server := range servers {
			if deletedSet[strings.TrimSpace(server.Name)] {
				// User deleted this name; do not resurrect from migration sources.
				continue
			}
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
	home := config.HomeDir()
	if strings.TrimSpace(home) == "" {
		return nil
	}
	return []migrationSource{
		{Path: filepath.Join(home, ".claude.json"), Source: "claude-user", Priority: 2},
		{Path: filepath.Join(home, ".claude", "mcp.json"), Source: "claude-user-legacy", Priority: 1},
	}
}

func loadClaudeServers(source migrationSource) ([]config.MCPServerConfig, []string, error) {
	data, err := os.ReadFile(source.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var parsed claudeConfigFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, nil, err
	}
	if len(parsed.MCPServers) == 0 {
		return nil, nil, nil
	}
	servers := make([]config.MCPServerConfig, 0, len(parsed.MCPServers))
	var warnings []string
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
		// #1276: every dropped entry must be VISIBLE - the old switch dropped
		// ws (fully supported by client+install!) and sse (unsupported) with a
		// bare default:continue, and even the stdio/http field checks failed
		// silently: users migrated, saw "success", and the server was simply
		// gone. Warnings name the entry, the transport, and the reason.
		switch cfg.Type {
		case "stdio":
			if strings.TrimSpace(cfg.Command) == "" {
				warnings = append(warnings, fmt.Sprintf("warning: skipped MCP server %q from %s (stdio transport requires a command)", cfg.Name, source.Source))
				continue
			}
		case "http", "ws":
			// ws is a first-class transport (install.go accepts stdio|http|ws,
			// client.go runs a full WS implementation) - it used to fall into
			// default:continue and vanish. Same URL requirement as http.
			if strings.TrimSpace(cfg.URL) == "" {
				warnings = append(warnings, fmt.Sprintf("warning: skipped MCP server %q from %s (%s transport requires a URL)", cfg.Name, source.Source, cfg.Type))
				continue
			}
		default:
			warnings = append(warnings, fmt.Sprintf("warning: skipped MCP server %q from %s (unsupported transport %q; supported: stdio, http, ws)", cfg.Name, source.Source, cfg.Type))
			continue
		}
		servers = append(servers, cfg)
	}
	return servers, warnings, nil
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
	case "http", "ws": // #1276: ws is URL-identified like http
		return cfg.Type + ":" + strings.TrimSpace(cfg.URL)
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
