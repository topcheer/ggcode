package mcp

import (
	"testing"
	"time"
)

// zz_issue4882_test.go pins the fix for #4882: listings cache invalidation
// was not durable against in-flight refills — a ListTools fetch that started
// BEFORE a tools/list_changed notification could complete AFTER the hot
// refresh and overwrite the fresh data with pre-notification listings until
// the server TTL expired.
//
// Fix shape: per-kind epoch bumped by invalidateKind/clear; every fetch
// captures the epoch before its network roundtrips and stores via
// putIfFresh (CAS) only when the epoch is unchanged.

// TestPutIfFreshDropsStaleRefill: the core CAS contract — a store carrying a
// pre-invalidation epoch must be dropped; a store with the current epoch must
// land.
func TestPutIfFreshDropsStaleRefill(t *testing.T) {
	lc := &listingCache{}
	cur := lc.epoch(cacheTools)

	// Pre-invalidation fetch completes after the notification landed.
	lc.invalidateKind(cacheTools)
	if got := lc.epoch(cacheTools); got == cur {
		t.Fatal("invalidateKind must bump the epoch")
	}
	lc.putIfFresh(cacheTools, "", []string{"stale"}, time.Minute, cur)
	if _, ok := lc.get(cacheTools, ""); ok {
		t.Fatal("#4882 regression: stale pre-invalidation refill overwrote the invalidation — cache serves pre-notification data until TTL")
	}

	// A fetch started after the invalidation stores normally.
	fresh := lc.epoch(cacheTools)
	lc.putIfFresh(cacheTools, "", []string{"fresh"}, time.Minute, fresh)
	v, ok := lc.get(cacheTools, "")
	s, sok := v.([]string)
	if !ok || !sok || s[0] != "fresh" {
		t.Fatalf("post-invalidation store must land: ok=%v v=%v", ok, v)
	}
}

// TestClearBumpsAllKindEpochs: clear() invalidates every kind, so an in-flight
// fetch of ANY kind must lose its CAS race against a whole-cache clear.
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
		lc.putIfFresh(k, "key", "stale", time.Minute, epochs[k]) // pre-clear fetch completes
		if _, ok := lc.get(k, "key"); ok {
			t.Fatalf("#4882: stale refill resurrected kind %d entry after clear", k)
		}
	}
}

// TestInvalidateKindIsolated: bumping one kind's epoch must not poison stores
// of other kinds (per-kind, not global, generation).
func TestInvalidateKindIsolated(t *testing.T) {
	lc := &listingCache{}
	toolsEpoch := lc.epoch(cacheTools)
	promptsEpoch := lc.epoch(cachePrompts)

	lc.invalidateKind(cacheTools) // only tools invalidated

	lc.putIfFresh(cachePrompts, "", "p", time.Minute, promptsEpoch)
	if _, ok := lc.get(cachePrompts, ""); !ok {
		t.Fatal("unrelated kind's epoch must be untouched by another kind's invalidation")
	}
	if toolsEpoch == lc.epoch(cacheTools) {
		t.Fatal("tools epoch must have bumped")
	}
}

// TestNotificationInvalidationBumpsEpoch: the listChanged notification path
// (cacheInvalidateForNotification) is the acceptance criterion from the issue
// ("a hot refresh observes fresh data") — after the notification, a store
// racing from before it must be dropped.
func TestNotificationInvalidationBumpsEpoch(t *testing.T) {
	c := &Client{}
	before := c.listingCache.epoch(cacheTools)
	if !c.cacheInvalidateForNotification("notifications/tools/list_changed", nil) {
		t.Fatal("tools/list_changed must match the notification handler")
	}
	if c.listingCache.epoch(cacheTools) == before {
		t.Fatal("notification invalidation must bump the tools epoch (#4882 acceptance criterion)")
	}
	// The stale in-flight fetch (captured pre-notification) completes now.
	pages := []CacheableResult{{TTLms: int64(time.Minute.Milliseconds()), CacheScope: "public"}}
	if ok := c.storeListingsCache(cacheTools, "", []string{"stale"}, pages, before); !ok {
		t.Fatal("result is cacheable per pages; the CAS drop must happen inside putIfFresh")
	}
	if _, hit := c.listingCache.get(cacheTools, ""); hit {
		t.Fatal("#4882 regression: stale refill landed after the notification — hot-refresh data overwritten until TTL")
	}
}
