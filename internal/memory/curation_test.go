package memory

import (
	"testing"
	"time"
)

func TestClassifyMemory(t *testing.T) {
	tests := []struct {
		key      string
		category MemoryCategory
	}{
		// Transient
		{"impl-task-2026-07-11-cycle1-summary", CategoryTransient},
		{"session-fix-bug", CategoryTransient},
		{"dedup-fix", CategoryTransient},
		{"connection-pool-bug", CategoryTransient},

		// Evolving
		{"competitor-analysis-2026-07-13-r3", CategoryEvolving},
		{"perf-research-2026-07-10", CategoryEvolving},
		{"ux-research-2026-07-12-r4", CategoryEvolving},
		{"frontier-papers-2026-07-14-r3", CategoryEvolving},

		// Persistent
		{"budget-guard-impl", CategoryPersistent},
		{"cache-keepalive-impl", CategoryPersistent},
		{"cron-persistence-design", CategoryPersistent},
		{"build-tags-goolm", CategoryPersistent},
		{"release-process", CategoryPersistent},

		// Default
		{"browser-tool-cdp", CategoryDefault},
		{"check-untracked-before-commit", CategoryDefault},
	}
	for _, tc := range tests {
		got := classifyMemory(tc.key)
		if got != tc.category {
			t.Errorf("classifyMemory(%q) = %q, want %q", tc.key, got, tc.category)
		}
	}
}

func TestDedupKey(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"competitor-analysis-2025-07", "competitor-analysis"},
		{"competitor-analysis-2026-07-10-r2", "competitor-analysis"},
		{"competitor-analysis-2026-07-11-r3", "competitor-analysis"},
		{"competitor-analysis-2026-07-13-r3", "competitor-analysis"},
		{"ux-research-2026-07-10", "ux-research"},
		{"ux-research-2026-07-12-r4", "ux-research"},
		{"impl-task-2026-07-11-cycle1-summary", "impl-task"},
		{"impl-task-2026-07-12-mcp-readonly", "impl-task-mcp-readonly"},
		{"desktop-ux-improvements-jul6", "desktop-ux-improvements"},
		{"desktop-ux-improvements-jul7-cycle2", "desktop-ux-improvements"},
		{"budget-guard-impl", "budget-guard-impl"}, // no date suffix
	}
	for _, tc := range tests {
		got := dedupKeyFor(tc.key)
		if got != tc.want {
			t.Errorf("dedupKeyFor(%q) = %q, want %q", tc.key, got, tc.want)
		}
	}
}

func TestShouldExpire(t *testing.T) {
	now := time.Now()

	// Transient: 31 days old → expired
	oldTransient := MemoryMeta{
		Key:       "impl-task-2026-06-01-foo",
		Category:  CategoryTransient,
		CreatedAt: now.Add(-31 * 24 * time.Hour),
	}
	if !shouldExpire(oldTransient, now) {
		t.Error("expected transient memory 31 days old to expire")
	}

	// Transient: 29 days old → not expired
	freshTransient := MemoryMeta{
		Key:       "impl-task-2026-07-01-foo",
		Category:  CategoryTransient,
		CreatedAt: now.Add(-29 * 24 * time.Hour),
	}
	if shouldExpire(freshTransient, now) {
		t.Error("expected transient memory 29 days old to NOT expire")
	}

	// Persistent: never expires
	persistent := MemoryMeta{
		Key:       "old-design",
		Category:  CategoryPersistent,
		CreatedAt: now.Add(-365 * 24 * time.Hour),
	}
	if shouldExpire(persistent, now) {
		t.Error("persistent memory should never expire")
	}
}

func TestCurateEntries(t *testing.T) {
	now := time.Now()
	day := 24 * time.Hour

	metas := []MemoryMeta{
		// Evolving: 3 versions of competitor-analysis, keep newest
		{Key: "competitor-analysis-2025-07", Category: CategoryEvolving, CreatedAt: now.Add(-400 * day), DedupKey: "competitor-analysis"},
		{Key: "competitor-analysis-2026-07-10-r2", Category: CategoryEvolving, CreatedAt: now.Add(-16 * day), DedupKey: "competitor-analysis"},
		{Key: "competitor-analysis-2026-07-13-r3", Category: CategoryEvolving, CreatedAt: now.Add(-13 * day), DedupKey: "competitor-analysis"},

		// Transient: old one expires, recent one survives
		{Key: "impl-task-2026-06-01-foo", Category: CategoryTransient, CreatedAt: now.Add(-55 * day)},
		{Key: "impl-task-2026-07-20-bar", Category: CategoryTransient, CreatedAt: now.Add(-6 * day)},

		// Persistent: never expires
		{Key: "budget-guard-impl", Category: CategoryPersistent, CreatedAt: now.Add(-100 * day)},
		{Key: "build-tags-goolm", Category: CategoryPersistent, CreatedAt: now.Add(-200 * day)},

		// Default
		{Key: "check-untracked-before-commit", Category: CategoryDefault, CreatedAt: now.Add(-50 * day)},
	}

	active, expired, deduped, capped := curateEntries(metas, now)

	// Expected: 1 newest competitor + 1 fresh impl-task + 2 persistent + 1 default = 5
	if len(active) != 5 {
		t.Fatalf("expected 5 active memories, got %d: %+v", len(active), active)
	}

	if expired != 1 {
		t.Errorf("expected 1 expired, got %d", expired)
	}

	if deduped != 2 {
		t.Errorf("expected 2 deduped (old competitor versions), got %d", deduped)
	}

	if capped != 0 {
		t.Errorf("expected 0 capped, got %d", capped)
	}

	// Verify newest competitor-analysis is kept
	foundNewest := false
	for _, m := range active {
		if m.Key == "competitor-analysis-2026-07-13-r3" {
			foundNewest = true
		}
		if m.Key == "competitor-analysis-2025-07" {
			t.Error("old competitor-analysis should be deduped out")
		}
	}
	if !foundNewest {
		t.Error("newest competitor-analysis should be in active list")
	}
}

func TestCapByCount(t *testing.T) {
	tests := []struct {
		name     string
		active   []MemoryMeta
		max      int
		wantLen  int
		wantCap  int
		evictedK []string // keys that must NOT be in result
		keptK    []string // keys that must be in result
	}{
		{
			name:    "under limit — no eviction",
			active:  []MemoryMeta{{Key: "a", Category: CategoryDefault}},
			max:     5,
			wantLen: 1,
			wantCap: 0,
		},
		{
			name: "exactly at limit — no eviction",
			active: []MemoryMeta{
				{Key: "a", Category: CategoryDefault},
				{Key: "b", Category: CategoryDefault},
			},
			max:     2,
			wantLen: 2,
			wantCap: 0,
		},
		{
			name: "over limit — evict oldest defaults",
			active: []MemoryMeta{
				{Key: "old1", Category: CategoryDefault, CreatedAt: time.Unix(100, 0)},
				{Key: "old2", Category: CategoryDefault, CreatedAt: time.Unix(200, 0)},
				{Key: "new1", Category: CategoryDefault, CreatedAt: time.Unix(300, 0)},
				{Key: "persist1", Category: CategoryPersistent, CreatedAt: time.Unix(150, 0)},
			},
			max:      3,
			wantLen:  3,
			wantCap:  1,
			evictedK: []string{"old1"}, // oldest default evicted
			keptK:    []string{"old2", "new1", "persist1"},
		},
		{
			name: "overflow exceeds defaults — evict all defaults, keep protected",
			active: []MemoryMeta{
				{Key: "d1", Category: CategoryDefault, CreatedAt: time.Unix(100, 0)},
				{Key: "d2", Category: CategoryDefault, CreatedAt: time.Unix(200, 0)},
				{Key: "p1", Category: CategoryPersistent},
				{Key: "p2", Category: CategoryPersistent},
				{Key: "p3", Category: CategoryPersistent},
			},
			max:      2,
			wantLen:  3, // can't go below 3 persistent
			wantCap:  2, // evicted both defaults
			keptK:    []string{"p1", "p2", "p3"},
			evictedK: []string{"d1", "d2"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, capped := capByCount(tc.active, tc.max)
			if len(result) != tc.wantLen {
				t.Fatalf("len=%d, want %d", len(result), tc.wantLen)
			}
			if capped != tc.wantCap {
				t.Errorf("capped=%d, want %d", capped, tc.wantCap)
			}

			rset := make(map[string]bool, len(result))
			for _, m := range result {
				rset[m.Key] = true
			}
			for _, k := range tc.evictedK {
				if rset[k] {
					t.Errorf("key %q should have been evicted", k)
				}
			}
			for _, k := range tc.keptK {
				if !rset[k] {
					t.Errorf("key %q should have been kept", k)
				}
			}
		})
	}
}

func TestCurateEntries_CapEvictsOldestDefault(t *testing.T) {
	now := time.Now()

	// Build 55 default memories (oldest to newest) + 10 persistent = 65 total.
	// maxActiveMemories=60 → should evict 5 oldest defaults, keep 50 defaults
	// and all 10 persistent = 60.
	var metas []MemoryMeta
	for i := 0; i < 55; i++ {
		metas = append(metas, MemoryMeta{
			Key:       "default-" + itoa(i),
			Category:  CategoryDefault,
			CreatedAt: now.Add(-time.Duration(55-i) * time.Hour),
		})
	}
	for i := 0; i < 10; i++ {
		metas = append(metas, MemoryMeta{
			Key:       "persist-" + itoa(i) + "-impl",
			Category:  CategoryPersistent,
			CreatedAt: now.Add(-time.Duration(i) * time.Hour),
		})
	}

	active, _, _, capped := curateEntries(metas, now)
	if len(active) != 60 {
		t.Fatalf("expected 60 active, got %d", len(active))
	}
	if capped != 5 {
		t.Fatalf("expected 5 capped, got %d", capped)
	}

	activeKeys := make(map[string]bool)
	for _, m := range active {
		activeKeys[m.Key] = true
	}
	// Oldest 5 defaults (default-0 through default-4) should be evicted.
	for i := 0; i < 5; i++ {
		if activeKeys["default-"+itoa(i)] {
			t.Errorf("default-%d should have been evicted (too old)", i)
		}
	}
	// Newest 50 defaults should survive.
	for i := 5; i < 55; i++ {
		if !activeKeys["default-"+itoa(i)] {
			t.Errorf("default-%d should have been kept (recent)", i)
		}
	}
	// All persistent memories must survive.
	for i := 0; i < 10; i++ {
		if !activeKeys["persist-"+itoa(i)+"-impl"] {
			t.Errorf("persist-%d-impl should never be evicted", i)
		}
	}
}
