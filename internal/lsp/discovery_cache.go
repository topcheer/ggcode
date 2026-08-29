package lsp

import (
	"sync"
	"time"
)

// External-toolchain probe cache.
//
// resolveManagedBinary's fallback switch spawns child processes to locate
// toolchain-managed servers (npm config get prefix, rustup which, dotnet
// tool probes). On Windows each spawn is a cmd.exe + runtime chain costing
// 1-3s, and discovery runs per edit (diagnostic baseline capture + post-edit
// diagnostics each resolve the server). Without caching, a workspace whose
// server is NOT installed pays the probe cost on every single edit - which
// is also how "npm config get prefix" ended up owning the terminal tab
// title for whole streams (fixed separately in 1f770241).
//
// Both positive AND negative results are cached: a negative probe re-spawn
// is pure waste, and a mid-session global install is rare enough that a
// 10-minute staleness window is an acceptable trade. PATH lookups
// (firstAvailableBinary) and workspace node_modules/.bin checks stay
// uncached - they are cheap stats and catch fresh local installs.

const probeCacheTTL = 10 * time.Minute

type probeCacheEntry struct {
	display string
	command string
	ok      bool
	expiry  time.Time
}

var probeCache = struct {
	sync.Mutex
	m map[string]probeCacheEntry
}{m: make(map[string]probeCacheEntry)}

// cachedExternalProbe memoizes probeExternalToolchain per (spec, workspace)
// with a TTL. Key includes the workspace so per-project tool installs
// (venv, node_modules) never collide across projects.
func cachedExternalProbe(spec serverSpec, workspace string) (display string, command string, ok bool) {
	key := spec.id + "\x00" + workspace
	now := time.Now()

	probeCache.Lock()
	if e, hit := probeCache.m[key]; hit && now.Before(e.expiry) {
		probeCache.Unlock()
		return e.display, e.command, e.ok
	}
	probeCache.Unlock()

	display, command, ok = probeExternalToolchain(spec, workspace)

	probeCache.Lock()
	probeCache.m[key] = probeCacheEntry{display, command, ok, now.Add(probeCacheTTL)}
	probeCache.Unlock()
	return display, command, ok
}
