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

	active, expired, deduped := curateEntries(metas, now)

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
