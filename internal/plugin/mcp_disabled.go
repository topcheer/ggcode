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

// MCP disabled-state storage (#2390).
//
// The store is scoped, not a global bare-name list: a server disabled inside
// workspace A (e.g. a broken stdio "github") must not silently disable a
// same-name server in workspace B or in the global scope. Reads consult the
// global bucket plus the caller's workspace bucket; writes land in the
// caller's bucket (enabling also clears the global entry so the panel's
// toggle is decisive regardless of which bucket disabled the server).
//
// File format (new):
//
//	{"global": ["name"], "workspaces": {"/abs/ws/path": ["name"]}}
//
// Legacy format (pre-#2390) was a bare ["name"] array. On read every legacy
// entry is folded into the global bucket - preserving the old "disables
// everywhere" semantics for state written before this change - and the file
// upgrades to the new shape on the next write.
type disabledStore struct {
	Global     []string            `json:"global,omitempty"`
	Workspaces map[string][]string `json:"workspaces,omitempty"`
}

var (
	mcpDisabledMu sync.RWMutex
	// mcpDisabledGlobal is the global-bucket view; mcpDisabledWS maps a
	// workspace path to its bucket. Both are only authoritative when
	// mcpDisabledCacheOK is set (#781: failures are never cached as truth).
	mcpDisabledGlobal  map[string]bool
	mcpDisabledWS      map[string]map[string]bool
	mcpDisabledCacheOK bool
)

func mcpDisabledPath() (string, error) {
	home := config.HomeDir()
	return filepath.Join(home, ".ggcode", "disabled_mcp.json"), nil
}

// decodeDisabledStore parses the on-disk file into bucket views. It returns
// the buckets plus whether the parse is cacheable-as-truth (a missing file
// is legitimately empty; read/decode failures are NOT - #781, corrupt JSON
// must not become truth).
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
		// Legacy bare-name array: every entry keeps its old global
		// (disables-everywhere) semantics.
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

// loadMCPDisabledBuckets returns the bucket views, hydrating the cache on
// first use. Never caches non-not-exist failures (#781).
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
		// #781: never cache non-not-exist failures -- a transient error
		// during AtomicWriteFile's rename window poisoned the cache and a
		// later SetMCPDisabledIn wrote it back as the full truth, silently
		// re-enabling previously disabled servers.
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

// MCPDisabledIn reports whether the named MCP server is disabled for the
// given scope: an empty scope consults only the global bucket (the
// pre-#2390 semantics); a non-empty scope additionally consults that
// workspace's own bucket.
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

// SetMCPDisabledIn persists the enabled/disabled state for an MCP server in
// the given scope (empty scope = the global bucket).
func SetMCPDisabledIn(scope, name string, disabled bool) error {
	// #1601-C: hold the WRITE lock across the whole read-modify-write (see
	// MCPDisabledIn readers + concurrent panel toggles).
	mcpDisabledMu.Lock()
	defer mcpDisabledMu.Unlock()

	global := mcpDisabledGlobal
	ws := mcpDisabledWS
	if !mcpDisabledCacheOK {
		// Cold cache: hydrate from disk under THIS lock (mirror the load
		// path's decode without re-locking). #1782 case 1: only a missing
		// file hydrates as legitimately empty; read/decode failures leave
		// the cache cold so the next reader retries instead of caching an
		// empty truth.
		global = map[string]bool{}
		ws = map[string]map[string]bool{}
		if path, err := mcpDisabledPath(); err == nil {
			g, w, cacheable := decodeDisabledStore(path)
			if cacheable {
				global, ws = g, w
			}
		}
		mcpDisabledGlobal = global
		mcpDisabledWS = ws
		if path, err := mcpDisabledPath(); err == nil {
			if _, rerr := os.ReadFile(path); rerr == nil || os.IsNotExist(rerr) {
				mcpDisabledCacheOK = true
			}
		}
	} else {
		// Warm cache: copy before mutating - readers index the cached maps
		// outside their RLock (MCPDisabledIn), so in-place mutation under
		// the write lock would still race them.
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
		// Enabling must clear EVERY bucket the panel toggle could have been
		// answering: a workspace-scope enable of a globally-disabled server
		// removes both entries, otherwise the server stays disabled after
		// the UI already reported success (the #408 lie in storage form).
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
		// #1740 case 2: keep the in-memory verdict (it governs this
		// process) but REPORT the persist failure so the caller can
		// surface it.
		mcpDisabledGlobal = global
		mcpDisabledWS = ws
		return fmt.Errorf("persist MCP disabled set: %w", werr)
	}

	mcpDisabledGlobal = global
	mcpDisabledWS = ws
	return nil
}
