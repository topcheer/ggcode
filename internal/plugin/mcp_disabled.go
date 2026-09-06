package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/util"
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
		// #781: never cache non-not-exist failures -- a transient error
		// during AtomicWriteFile's rename window poisoned the cache and a
		// later SetMCPDisabled wrote it back as the full truth, silently
		// re-enabling previously disabled servers.
		return map[string]bool{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			mcpDisabledCache = map[string]bool{}
			mcpDisabledCacheOK = true
			return mcpDisabledCache
		}
		return map[string]bool{}
	}
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		// #781: corrupt JSON is also not cacheable as truth.
		return map[string]bool{}
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
	// #1601-C: hold the WRITE lock across the whole read-modify-write.
	// The old flow snapshotted the cache (RLock inside
	// loadMCPDisabledSet), mutated the copy, then re-locked to store -
	// two concurrent toggles based on the same stale snapshot lost the
	// first one's update in memory AND on disk (panel rapid-toggling of
	// multiple servers). With the write lock held, the nested
	// loadMCPDisabledSet takes its RLock on the same goroutine -> would
	// deadlock, so read the cache under THIS lock directly.
	mcpDisabledMu.Lock()
	defer mcpDisabledMu.Unlock()
	cached := mcpDisabledCache
	if !mcpDisabledCacheOK {
		// Cold cache: hydrate from disk (lock already held; mirror the
		// load path's decode without re-locking).
		cached = map[string]bool{}
		if path, err := mcpDisabledPath(); err == nil {
			if data, rerr := os.ReadFile(path); rerr == nil {
				var names []string
				if jerr := json.Unmarshal(data, &names); jerr == nil {
					// #781: corrupt JSON is not cacheable as truth.
					cached = make(map[string]bool, len(names))
					for _, n := range names {
						cached[n] = true
					}
				}
			}
		}
		mcpDisabledCache = cached
		mcpDisabledCacheOK = true
	} else {
		// Warm cache: copy before mutating - readers index the cached
		// map pointer outside their RLock (MCPDisabled), so in-place
		// mutation under the write lock would still race them.
		cp := make(map[string]bool, len(cached)+1)
		for k, v := range cached {
			cp[k] = v
		}
		cached = cp
	}
	if disabled {
		cached[name] = true
	} else {
		delete(cached, name)
	}
	path, err := mcpDisabledPath()
	if err != nil {
		return
	}
	names := make([]string, 0, len(cached))
	for n, v := range cached {
		if v {
			names = append(names, n)
		}
	}
	data, _ := json.MarshalIndent(names, "", "  ")
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = util.AtomicWriteFile(path, data, 0o600)

	mcpDisabledCache = cached
}
