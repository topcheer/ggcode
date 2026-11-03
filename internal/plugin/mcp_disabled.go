package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/util"
)

type disabledStore struct {
	Global     []string            `json:"global,omitempty"`
	Workspaces map[string][]string `json:"workspaces,omitempty"`
}

var (
	mcpDisabledMu sync.RWMutex

	mcpDisabledGlobal  map[string]bool
	mcpDisabledWS      map[string]map[string]bool
	mcpDisabledCacheOK bool
)

func mcpDisabledPath() (string, error) {
	home := config.HomeDir()
	return filepath.Join(home, ".ggcode", "disabled_mcp.json"), nil
}

func decodeDisabledStore(path string) (global map[string]bool, ws map[string]map[string]bool, cacheable bool) {
	global = map[string]bool{}
	ws = map[string]map[string]bool{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return global, ws, true
		}
		return global, ws, false
	}
	trimmed := leadingJSONWhitespace(data)
	if len(trimmed) > 0 && trimmed[0] == '[' {

		var names []string
		if err := json.Unmarshal(data, &names); err != nil {
			return global, ws, false
		}
		for _, n := range names {
			global[n] = true
		}
		return global, ws, true
	}
	var store disabledStore
	if err := json.Unmarshal(data, &store); err != nil {
		return global, ws, false
	}
	for _, n := range store.Global {
		global[n] = true
	}
	for wsPath, names := range store.Workspaces {
		bucket := make(map[string]bool, len(names))
		for _, n := range names {
			bucket[n] = true
		}
		ws[wsPath] = bucket
	}
	return global, ws, true
}

func leadingJSONWhitespace(data []byte) []byte {
	for i, b := range data {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return data[i:]
		}
	}
	return nil
}

func loadMCPDisabledBuckets() (map[string]bool, map[string]map[string]bool) {
	mcpDisabledMu.RLock()
	if mcpDisabledCacheOK {
		g, w := mcpDisabledGlobal, mcpDisabledWS
		mcpDisabledMu.RUnlock()
		return g, w
	}
	mcpDisabledMu.RUnlock()

	mcpDisabledMu.Lock()
	defer mcpDisabledMu.Unlock()
	if mcpDisabledCacheOK {
		return mcpDisabledGlobal, mcpDisabledWS
	}

	path, err := mcpDisabledPath()
	if err != nil {

		return map[string]bool{}, map[string]map[string]bool{}
	}
	global, ws, cacheable := decodeDisabledStore(path)
	if !cacheable {
		return map[string]bool{}, map[string]map[string]bool{}
	}
	mcpDisabledGlobal = global
	mcpDisabledWS = ws
	mcpDisabledCacheOK = true
	return mcpDisabledGlobal, mcpDisabledWS
}

func MCPDisabledIn(scope, name string) bool {
	global, ws := loadMCPDisabledBuckets()
	if global[name] {
		return true
	}
	if scope == "" {
		return false
	}
	if bucket, ok := ws[scope]; ok && bucket[name] {
		return true
	}
	return false
}

func SetMCPDisabledIn(scope, name string, disabled bool) error {

	mcpDisabledMu.Lock()
	defer mcpDisabledMu.Unlock()

	global := mcpDisabledGlobal
	ws := mcpDisabledWS
	if !mcpDisabledCacheOK {

		global = map[string]bool{}
		ws = map[string]map[string]bool{}
		if path, err := mcpDisabledPath(); err == nil {
			g, w, cacheable := decodeDisabledStore(path)
			if cacheable {
				global, ws = g, w
				mcpDisabledCacheOK = true
			}
		}
		mcpDisabledGlobal = global
		mcpDisabledWS = ws
	} else {

		g := make(map[string]bool, len(global)+1)
		for k, v := range global {
			g[k] = v
		}
		w := make(map[string]map[string]bool, len(ws)+1)
		for k, bucket := range ws {
			cp := make(map[string]bool, len(bucket))
			for n, v := range bucket {
				cp[n] = v
			}
			w[k] = cp
		}
		global, ws = g, w
	}

	if disabled {
		if scope == "" {
			global[name] = true
		} else {
			bucket, ok := ws[scope]
			if !ok {
				bucket = map[string]bool{}
				ws[scope] = bucket
			}
			bucket[name] = true
		}
	} else {

		delete(global, name)
		if scope != "" {
			if bucket, ok := ws[scope]; ok {
				delete(bucket, name)
				if len(bucket) == 0 {
					delete(ws, scope)
				}
			}
		}
	}

	path, err := mcpDisabledPath()
	if err != nil {
		mcpDisabledGlobal = global
		mcpDisabledWS = ws
		return fmt.Errorf("mcp disabled path: %w", err)
	}
	store := disabledStore{Workspaces: map[string][]string{}}
	for n, v := range global {
		if v {
			store.Global = append(store.Global, n)
		}
	}
	for wsPath, bucket := range ws {
		names := make([]string, 0, len(bucket))
		for n, v := range bucket {
			if v {
				names = append(names, n)
			}
		}
		if len(names) > 0 {
			store.Workspaces[wsPath] = names
		}
	}
	data, _ := json.MarshalIndent(store, "", "  ")
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	if werr := util.AtomicWriteFile(path, data, 0o600); werr != nil {

		mcpDisabledGlobal = global
		mcpDisabledWS = ws
		return fmt.Errorf("persist MCP disabled set: %w", werr)
	}

	mcpDisabledGlobal = global
	mcpDisabledWS = ws
	return nil
}
