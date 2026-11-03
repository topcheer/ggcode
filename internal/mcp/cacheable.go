package mcp

import (
	"encoding/json"
	"sync"
	"time"
)

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

type listingCache struct {
	mu    sync.Mutex
	items map[listingCacheKind]map[string]listingCacheEntry

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

func (c *Client) storeListingsCache(kind listingCacheKind, key string, value any, pages []CacheableResult, fetchEpoch uint64) bool {
	ttl, _, ok := listingCacheTTL(pages)
	if !ok {
		return false
	}
	c.listingCache.putIfFresh(kind, key, value, ttl, fetchEpoch)
	return true
}

func cloneCachedSlice[T any](s []T) []T {
	return append(make([]T, 0, len(s)), s...)
}

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

func (c *Client) InvalidateListingsCache() {
	c.listingCache.clear()
}
