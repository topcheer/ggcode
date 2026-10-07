package memory

// Usage-aware count-cap eviction (research sa-116, interference-based
// forgetting per Microsoft Human-Inspired Memory 2026 / arXiv:2603.07670
// 9.4): capByCountSplit must evict never-injected (Uses==0) default
// entries before any entry with a retrieval history, regardless of age.

import (
	"testing"
	"time"
)

func metaFor(key string, created time.Time, uses int) MemoryMeta {
	return MemoryMeta{
		Key:       key,
		Category:  CategoryDefault,
		CreatedAt: created,
		Uses:      uses,
	}
}

func TestUsageAwareCap_NeverUsedEvictedBeforeUsed(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	active := []MemoryMeta{
		metaFor("old-used", base, 7),                   // oldest but frequently injected
		metaFor("mid-never", base.AddDate(0, 1, 0), 0), // never injected
		metaFor("new-never", base.AddDate(0, 2, 0), 0), // never injected
	}
	kept, evicted := capByCountSplit(active, 2)
	if len(kept) != 2 || len(evicted) != 1 {
		t.Fatalf("kept=%d evicted=%d, want 2/1", len(kept), len(evicted))
	}
	if evicted[0].Key != "mid-never" {
		t.Fatalf("never-used entry must be evicted before used one, evicted=%q", evicted[0].Key)
	}
	for _, m := range kept {
		if m.Key == "mid-never" {
			t.Fatalf("mid-never must not be kept")
		}
	}
	if keptContains(kept, "old-used") == false {
		t.Fatalf("old-used (Uses=7) must survive despite being oldest")
	}
}

func TestUsageAwareCap_AllNeverUsedFallsBackToOldest(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	active := []MemoryMeta{
		metaFor("b", base.AddDate(0, 1, 0), 0),
		metaFor("a", base, 0),
		metaFor("c", base.AddDate(0, 2, 0), 0),
	}
	_, evicted := capByCountSplit(active, 2)
	if len(evicted) != 1 || evicted[0].Key != "a" {
		t.Fatalf("all never-used: oldest must go, got %v", keysOf(evicted))
	}
}

func TestUsageAwareCap_UsedEvictedOnlyWhenNoNeverUsedLeft(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	active := []MemoryMeta{
		metaFor("used-old", base, 3),
		metaFor("used-new", base.AddDate(0, 1, 0), 1),
	}
	// No never-used entries: pure CreatedAt order decides among used ones.
	_, evicted := capByCountSplit(active, 1)
	if len(evicted) != 1 || evicted[0].Key != "used-old" {
		t.Fatalf("both used: oldest used must go, got %v", keysOf(evicted))
	}
}

func TestUsageAwareCap_DeterministicTiebreak(t *testing.T) {
	ts := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
	active := []MemoryMeta{
		metaFor("zzz", ts, 0),
		metaFor("aaa", ts, 0),
	}
	_, evicted := capByCountSplit(active, 1)
	if len(evicted) != 1 || evicted[0].Key != "aaa" {
		t.Fatalf("same CreatedAt: key order tiebreak expected aaa, got %v", keysOf(evicted))
	}
}

func keysOf(ms []MemoryMeta) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Key
	}
	return out
}

func keptContains(ms []MemoryMeta, key string) bool {
	for _, m := range ms {
		if m.Key == key {
			return true
		}
	}
	return false
}
