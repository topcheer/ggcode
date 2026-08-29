package lsp

import (
	"testing"
	"time"
)

// The cache must serve unexpired entries verbatim (no probe re-run) and
// route expired entries back through probeExternalToolchain.
func TestCachedExternalProbeTTL(t *testing.T) {
	probeCache.Lock()
	probeCache.m = make(map[string]probeCacheEntry)
	probeCache.Unlock()
	t.Cleanup(func() {
		probeCache.Lock()
		probeCache.m = make(map[string]probeCacheEntry)
		probeCache.Unlock()
	})

	spec := serverSpec{id: "definitely-unknown-spec"}
	ws := t.TempDir()

	// Unknown spec id probes to (","",false) and caches the negative.
	d, c, ok := cachedExternalProbe(spec, ws)
	if ok || d != "" || c != "" {
		t.Fatalf("unknown spec must probe negative, got (%q,%q,%v)", d, c, ok)
	}

	// Inject a fresh positive entry for the same key: must be served as-is,
	// proving the probe is not re-run (probe would return the negative above).
	probeCache.Lock()
	probeCache.m[spec.id+"\x00"+ws] = probeCacheEntry{
		display: "cached-bin", command: "/cached/path", ok: true,
		expiry: time.Now().Add(probeCacheTTL),
	}
	probeCache.Unlock()
	if d, c, ok = cachedExternalProbe(spec, ws); !ok || d != "cached-bin" || c != "/cached/path" {
		t.Fatalf("unexpired entry must be served verbatim, got (%q,%q,%v)", d, c, ok)
	}

	// Expired entry: probe runs again and the negative overwrites the stale
	// positive.
	probeCache.Lock()
	e := probeCache.m[spec.id+"\x00"+ws]
	e.expiry = time.Now().Add(-time.Second)
	probeCache.m[spec.id+"\x00"+ws] = e
	probeCache.Unlock()
	if d, c, ok = cachedExternalProbe(spec, ws); ok || d != "" || c != "" {
		t.Fatalf("expired entry must re-probe, got (%q,%q,%v)", d, c, ok)
	}
}

// Distinct workspaces must never share probe results (per-project installs).
func TestCachedExternalProbeWorkspaceIsolation(t *testing.T) {
	probeCache.Lock()
	probeCache.m = make(map[string]probeCacheEntry)
	probeCache.Unlock()
	t.Cleanup(func() {
		probeCache.Lock()
		probeCache.m = make(map[string]probeCacheEntry)
		probeCache.Unlock()
	})

	spec := serverSpec{id: "definitely-unknown-spec"}
	ws1, ws2 := t.TempDir(), t.TempDir()
	probeCache.Lock()
	probeCache.m[spec.id+"\x00"+ws1] = probeCacheEntry{
		display: "ws1-bin", ok: true, expiry: time.Now().Add(probeCacheTTL),
	}
	probeCache.Unlock()

	if _, _, ok := cachedExternalProbe(spec, ws1); !ok {
		t.Fatal("ws1 must hit its cached entry")
	}
	if _, _, ok := cachedExternalProbe(spec, ws2); ok {
		t.Fatal("ws2 must not inherit ws1's cache entry")
	}
}
