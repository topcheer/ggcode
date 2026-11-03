package lsp

import (
	"testing"
	"time"
)

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

	d, c, ok := cachedExternalProbe(spec, ws)
	if ok || d != "" || c != "" {
		t.Fatalf("unknown spec must probe negative, got (%q,%q,%v)", d, c, ok)
	}

	probeCache.Lock()
	probeCache.m[spec.id+"\x00"+ws] = probeCacheEntry{
		display: "cached-bin", command: "/cached/path", ok: true,
		expiry: time.Now().Add(probeCacheTTL),
	}
	probeCache.Unlock()
	if d, c, ok = cachedExternalProbe(spec, ws); !ok || d != "cached-bin" || c != "/cached/path" {
		t.Fatalf("unexpired entry must be served verbatim, got (%q,%q,%v)", d, c, ok)
	}

	probeCache.Lock()
	e := probeCache.m[spec.id+"\x00"+ws]
	e.expiry = time.Now().Add(-time.Second)
	probeCache.m[spec.id+"\x00"+ws] = e
	probeCache.Unlock()
	if d, c, ok = cachedExternalProbe(spec, ws); ok || d != "" || c != "" {
		t.Fatalf("expired entry must re-probe, got (%q,%q,%v)", d, c, ok)
	}
}

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
