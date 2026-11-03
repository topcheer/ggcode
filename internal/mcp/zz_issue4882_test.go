package mcp

import (
	"testing"
	"time"
)

func TestPutIfFreshDropsStaleRefill(t *testing.T) {
	lc := &listingCache{}
	cur := lc.epoch(cacheTools)

	lc.invalidateKind(cacheTools)
	if got := lc.epoch(cacheTools); got == cur {
		t.Fatal("invalidateKind must bump the epoch")
	}
	lc.putIfFresh(cacheTools, "", []string{"stale"}, time.Minute, cur)
	if _, ok := lc.get(cacheTools, ""); ok {
		t.Fatal("#4882 regression: stale pre-invalidation refill overwrote the invalidation — cache serves pre-notification data until TTL")
	}

	fresh := lc.epoch(cacheTools)
	lc.putIfFresh(cacheTools, "", []string{"fresh"}, time.Minute, fresh)
	v, ok := lc.get(cacheTools, "")
	s, sok := v.([]string)
	if !ok || !sok || s[0] != "fresh" {
		t.Fatalf("post-invalidation store must land: ok=%v v=%v", ok, v)
	}
}

func TestClearBumpsAllKindEpochs(t *testing.T) {
	lc := &listingCache{}
	kinds := []listingCacheKind{cacheTools, cachePrompts, cacheResources, cacheResourceRead, cacheResourceTemplates}
	epochs := map[listingCacheKind]uint64{}
	for _, k := range kinds {
		epochs[k] = lc.epoch(k)
		lc.putIfFresh(k, "key", "seed", time.Minute, epochs[k])
	}
	lc.clear()
	for _, k := range kinds {
		if lc.epoch(k) == epochs[k] {
			t.Fatalf("clear must bump epoch for kind %d", k)
		}
		if _, ok := lc.get(k, "key"); ok {
			t.Fatalf("clear must drop entries for kind %d", k)
		}
		lc.putIfFresh(k, "key", "stale", time.Minute, epochs[k])
		if _, ok := lc.get(k, "key"); ok {
			t.Fatalf("#4882: stale refill resurrected kind %d entry after clear", k)
		}
	}
}

func TestInvalidateKindIsolated(t *testing.T) {
	lc := &listingCache{}
	toolsEpoch := lc.epoch(cacheTools)
	promptsEpoch := lc.epoch(cachePrompts)

	lc.invalidateKind(cacheTools)

	lc.putIfFresh(cachePrompts, "", "p", time.Minute, promptsEpoch)
	if _, ok := lc.get(cachePrompts, ""); !ok {
		t.Fatal("unrelated kind's epoch must be untouched by another kind's invalidation")
	}
	if toolsEpoch == lc.epoch(cacheTools) {
		t.Fatal("tools epoch must have bumped")
	}
}

func TestNotificationInvalidationBumpsEpoch(t *testing.T) {
	c := &Client{}
	before := c.listingCache.epoch(cacheTools)
	if !c.cacheInvalidateForNotification("notifications/tools/list_changed", nil) {
		t.Fatal("tools/list_changed must match the notification handler")
	}
	if c.listingCache.epoch(cacheTools) == before {
		t.Fatal("notification invalidation must bump the tools epoch (#4882 acceptance criterion)")
	}

	pages := []CacheableResult{{TTLms: int64(time.Minute.Milliseconds()), CacheScope: "public"}}
	if ok := c.storeListingsCache(cacheTools, "", []string{"stale"}, pages, before); !ok {
		t.Fatal("result is cacheable per pages; the CAS drop must happen inside putIfFresh")
	}
	if _, hit := c.listingCache.get(cacheTools, ""); hit {
		t.Fatal("#4882 regression: stale refill landed after the notification — hot-refresh data overwritten until TTL")
	}
}
