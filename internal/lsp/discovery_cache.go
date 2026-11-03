package lsp

import (
	"sync"
	"time"
)

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

func InvalidateProbeCache() {
	probeCache.Lock()
	defer probeCache.Unlock()
	probeCache.m = make(map[string]probeCacheEntry)
}

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
