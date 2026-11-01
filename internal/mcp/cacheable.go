package mcp

// MCP 2026-07-28 CacheableResult client-side freshness caching (SEP-2549).
//
// Spec-compliant 2026-07-28 servers attach ttlMs and cacheScope to every
// tools/list, prompts/list, resources/list and resources/read result. The
// ttlMs hint lets clients serve subsequent reads from cache instead of
// re-polling the server — the intended pairing with the listChanged
// notifications, which invalidate the cache when data actually changes.
// Servers predating 2026-07-28 never send the fields, so every helper here
// treats an absent or non-positive ttlMs as "do not cache" and behavior is
// byte-for-byte unchanged for them.

import (
	"encoding/json"
	"sync"
	"time"
)

// listingCacheKind identifies which request a cache entry belongs to.
type listingCacheKind int

const (
	cacheTools listingCacheKind = iota
	cachePrompts
	cacheResources
	cacheResourceRead
	cacheResourceTemplates
)

type listingCacheEntry struct {
	value     any
	expiresAt time.Time
}

// listingCache is a per-Client freshness cache keyed by request kind (and URI
// for resources/read). The zero value is ready to use, so struct-literal
// clients (tests) never need initialization.
type listingCache struct {
	mu    sync.Mutex
	items map[listingCacheKind]map[string]listingCacheEntry
	// #4882: per-kind generation counter, bumped on every invalidation of
	// that kind (invalidateKind/clear). A fetch captures the epoch before its
	// network roundtrips and stores via putIfFresh only when the epoch is
	// unchanged — an in-flight fetch that started BEFORE an invalidation can
	// no longer overwrite the hot-refresh data the invalidation enabled
	// (previously the stale refill lived until the server TTL expired).
	epochs [cacheResourceTemplates + 1]uint64
}

func (lc *listingCache) get(kind listingCacheKind, key string) (any, bool) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	entry, ok := lc.items[kind][key]
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(lc.items[kind], key)
		return nil, false
	}
	return entry.value, true
}

func (lc *listingCache) epoch(kind listingCacheKind) uint64 {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	return lc.epochs[kind]
}

func (lc *listingCache) invalidateKind(kind listingCacheKind) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	delete(lc.items, kind)
	lc.epochs[kind]++
}

// invalidateResource drops one cached resources/read entry — the
// notifications/resources/updated refresh for a subscribed URI.
func (lc *listingCache) invalidateResource(uri string) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	delete(lc.items[cacheResourceRead], uri)
}

func (lc *listingCache) clear() {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.items = nil
	for i := range lc.epochs {
		lc.epochs[i]++
	}
}

// listingCacheTTL resolves the freshness window across the pages of one
// aggregated listing. Any page without a positive ttlMs disables caching
// entirely (pre-2026-07-28 servers omit the fields). The effective TTL is the
// minimum across pages, and the scope degrades conservatively to "private"
// when any page says so.
func listingCacheTTL(pages []CacheableResult) (ttl time.Duration, scope string, ok bool) {
	scope = "public"
	for _, p := range pages {
		if p.TTLms <= 0 {
			return 0, "", false
		}
		d := time.Duration(p.TTLms) * time.Millisecond
		if ttl == 0 || d < ttl {
			ttl = d
		}
		if p.CacheScope == "private" {
			scope = "private"
		}
	}
	return ttl, scope, ttl > 0
}

// putIfFresh stores value only when the kind's generation still equals
// fetchEpoch (CAS semantics, #4882). An invalidation that landed while the
// fetch was in flight bumped the epoch, so the stale refill is dropped and
// the cache stays coherent with the server's change notification.
func (lc *listingCache) putIfFresh(kind listingCacheKind, key string, value any, ttl time.Duration, fetchEpoch uint64) {
	if ttl <= 0 {
		return
	}
	lc.mu.Lock()
	defer lc.mu.Unlock()
	if lc.epochs[kind] != fetchEpoch {
		return
	}
	if lc.items == nil {
		lc.items = make(map[listingCacheKind]map[string]listingCacheEntry)
	}
	if lc.items[kind] == nil {
		lc.items[kind] = make(map[string]listingCacheEntry)
	}
	lc.items[kind][key] = listingCacheEntry{value: value, expiresAt: time.Now().Add(ttl)}
}

// storeListingsCache stores a fully fetched listing/read result under
// (kind, key) with the freshness window resolved from its result pages.
// Partial pagination failures never reach this (their early returns skip the
// store call), so only complete result sets are cached. fetchEpoch must be
// the kind's epoch captured BEFORE the fetch began; an invalidation that
// raced the fetch drops the store (#4882). Returns true when the result was
// cacheable (every page carried ttlMs).
func (c *Client) storeListingsCache(kind listingCacheKind, key string, value any, pages []CacheableResult, fetchEpoch uint64) bool {
	ttl, _, ok := listingCacheTTL(pages)
	if !ok {
		return false
	}
	c.listingCache.putIfFresh(kind, key, value, ttl, fetchEpoch)
	return true
}

// cloneCachedSlice returns a shallow copy so callers mutating the returned
// slice (sorting, filtering) cannot corrupt the cache entry.
func cloneCachedSlice[T any](s []T) []T {
	return append(make([]T, 0, len(s)), s...)
}

// cacheInvalidateForNotification drops cached entries affected by a server
// change notification — the listChanged family plus the resources/updated
// subscription refresh. It is the invalidation counterpart required for
// SEP-2549's freshness-hint cache to stay coherent. Returns true when the
// notification matched one of the cache-affecting methods.
func (c *Client) cacheInvalidateForNotification(method string, params json.RawMessage) bool {
	switch method {
	case "notifications/tools/list_changed":
		c.listingCache.invalidateKind(cacheTools)
		return true
	case "notifications/prompts/list_changed":
		c.listingCache.invalidateKind(cachePrompts)
		return true
	case "notifications/resources/list_changed":
		c.listingCache.invalidateKind(cacheResources)
		c.listingCache.invalidateKind(cacheResourceTemplates)
		return true
	case "notifications/resources/updated":
		var p struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(params, &p); err == nil && p.URI != "" {
			c.listingCache.invalidateResource(p.URI)
			return true
		}
	}
	return false
}

// InvalidateListingsCache drops every cached listing/read entry. The
// notification and re-initialize paths call the targeted invalidations
// automatically; this is the manual escape hatch for callers.
func (c *Client) InvalidateListingsCache() {
	c.listingCache.clear()
}
