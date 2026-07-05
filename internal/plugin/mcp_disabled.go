package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/topcheer/ggcode/internal/config"
)

var (
	mcpDisabledMu      sync.RWMutex
	mcpDisabledCache   map[string]bool
	mcpDisabledCacheOK bool
)

func mcpDisabledPath() (string, error) {
	home := config.HomeDir()
	return filepath.Join(home, ".ggcode", "disabled_mcp.json"), nil
}

func loadMCPDisabledSet() map[string]bool {
	mcpDisabledMu.RLock()
	if mcpDisabledCacheOK {
		c := mcpDisabledCache
		mcpDisabledMu.RUnlock()
		return c
	}
	mcpDisabledMu.RUnlock()

	mcpDisabledMu.Lock()
	defer mcpDisabledMu.Unlock()
	if mcpDisabledCacheOK {
		return mcpDisabledCache
	}

	path, err := mcpDisabledPath()
	if err != nil {
		mcpDisabledCache = map[string]bool{}
		mcpDisabledCacheOK = true
		return mcpDisabledCache
	}
	data, err := os.ReadFile(path)
	if err != nil {
		mcpDisabledCache = map[string]bool{}
		mcpDisabledCacheOK = true
		return mcpDisabledCache
	}
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		mcpDisabledCache = map[string]bool{}
		mcpDisabledCacheOK = true
		return mcpDisabledCache
	}
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	mcpDisabledCache = m
	mcpDisabledCacheOK = true
	return m
}

// MCPDisabled returns whether the named MCP server is disabled.
func MCPDisabled(name string) bool {
	return loadMCPDisabledSet()[name]
}

// SetMCPDisabled persists the enabled/disabled state for an MCP server.
func SetMCPDisabled(name string, disabled bool) {
	cached := loadMCPDisabledSet()
	// Copy the cached map before mutating to avoid concurrent map access
	// with readers that hold the same map pointer from the cache.
	disabledSet := make(map[string]bool, len(cached)+1)
	for k, v := range cached {
		disabledSet[k] = v
	}
	if disabled {
		disabledSet[name] = true
	} else {
		delete(disabledSet, name)
	}
	path, err := mcpDisabledPath()
	if err != nil {
		return
	}
	names := make([]string, 0, len(disabledSet))
	for n, v := range disabledSet {
		if v {
			names = append(names, n)
		}
	}
	data, _ := json.MarshalIndent(names, "", "  ")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, data, 0o644)

	mcpDisabledMu.Lock()
	mcpDisabledCache = disabledSet
	mcpDisabledMu.Unlock()
}
