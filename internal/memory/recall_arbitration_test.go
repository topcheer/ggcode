package memory

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMemoryTrustScore(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		meta MemoryMeta
		want float64
	}{
		{"persistent fresh", MemoryMeta{Category: CategoryPersistent, CreatedAt: now.Add(-24 * time.Hour)}, trustBase + trustPersistentBonus + trustFreshBonus},
		{"persistent ancient", MemoryMeta{Category: CategoryPersistent, CreatedAt: now.Add(-200 * 24 * time.Hour)}, trustBase + trustPersistentBonus - trustAncientPenalty},
		{"evolving fresh", MemoryMeta{Category: CategoryEvolving, CreatedAt: now.Add(-24 * time.Hour)}, trustBase - trustEvolvingPenalty + trustFreshBonus},
		{"default mid-age", MemoryMeta{Category: CategoryDefault, CreatedAt: now.Add(-30 * 24 * time.Hour)}, trustBase},
		{"future modtime clamps fresh bonus", MemoryMeta{Category: CategoryDefault, CreatedAt: now.Add(time.Hour)}, trustBase},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := memoryTrustScore(tc.meta, now)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("trust = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestArbitrateInline_ConflictPrefersHigherTrust(t *testing.T) {
	now := time.Now()
	old := MemoryEntry{
		Key:     "build-setup-impl",
		Content: "build command: go build ./...",
		Meta:    MemoryMeta{Key: "build-setup-impl", Category: CategoryPersistent, CreatedAt: now.Add(-90 * 24 * time.Hour)},
	}
	// Same category, newer modtime -> wins the tie on trust.
	newer := MemoryEntry{
		Key:     "build-tags-impl",
		Content: "build command: go build -tags goolm ./...",
		Meta:    MemoryMeta{Key: "build-tags-impl", Category: CategoryPersistent, CreatedAt: now.Add(-24 * time.Hour)},
	}

	arb := ArbitrateInline([]MemoryEntry{old, newer}, now)
	if !arb.HasConflicts() {
		t.Fatal("expected conflict to be detected")
	}
	if len(arb.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(arb.Conflicts))
	}
	c := arb.Conflicts[0]
	if c.Subject != "build command" {
		t.Fatalf("subject = %q", c.Subject)
	}
	if c.WinnerKey != newer.Key || c.LoserKey != old.Key {
		t.Fatalf("winner=%s loser=%s, want winner=%s loser=%s", c.WinnerKey, c.LoserKey, newer.Key, old.Key)
	}

	entries := []MemoryEntry{old, newer}
	arb.Annotate(entries)
	if !strings.Contains(entries[0].Content, "[memory-conflict]") {
		t.Fatalf("loser content not annotated:\n%s", entries[0].Content)
	}
	if strings.Contains(entries[1].Content, "[memory-conflict]") {
		t.Fatalf("winner content must stay clean:\n%s", entries[1].Content)
	}
}

func TestArbitrateInline_CategoryBeatsRecency(t *testing.T) {
	now := time.Now()
	// Default-category but ancient vs persistent-category but slightly older
	// in absolute terms: the persistent bonus (+0.2) must outweigh the
	// ancient penalty (-0.3) applied to the opponent... here the ancient
	// penalty lands ON the newer default entry, proving category wins.
	ancientPersistent := MemoryEntry{
		Key:     "arch-decision",
		Content: "database engine: postgres",
		Meta:    MemoryMeta{Category: CategoryPersistent, CreatedAt: now.Add(-100 * 24 * time.Hour)},
	}
	recentDefault := MemoryEntry{
		Key:     "scratch-note",
		Content: "database engine: sqlite",
		Meta:    MemoryMeta{Category: CategoryDefault, CreatedAt: now.Add(-80 * 24 * time.Hour)},
	}
	// scores: persistent 1.2-0=1.2 (ancient only above 180d); default 1.0.
	arb := ArbitrateInline([]MemoryEntry{recentDefault, ancientPersistent}, now)
	if !arb.HasConflicts() {
		t.Fatal("expected conflict between same-subject single-token values")
	}
	if got := arb.Conflicts[0].WinnerKey; got != ancientPersistent.Key {
		t.Fatalf("winner = %s, want persistent entry %s", got, ancientPersistent.Key)
	}
}

func TestArbitrateInline_NoFalsePositives(t *testing.T) {
	now := time.Now()
	entries := []MemoryEntry{
		{Key: "a", Content: "build command: go build ./...", Meta: MemoryMeta{Category: CategoryPersistent, CreatedAt: now}},
		{Key: "b", Content: "test runner: go test ./...", Meta: MemoryMeta{Category: CategoryPersistent, CreatedAt: now}},
		{Key: "c", Content: "framework: gin", Meta: MemoryMeta{Category: CategoryDefault, CreatedAt: now}},
	}
	arb := ArbitrateInline(entries, now)
	if arb.HasConflicts() {
		t.Fatalf("unexpected conflicts: %+v", arb.Conflicts)
	}
	// Identical values are duplicates, not conflicts.
	dup := MemoryEntry{Key: "d", Content: "framework: gin", Meta: MemoryMeta{Category: CategoryDefault, CreatedAt: now}}
	arb = ArbitrateInline([]MemoryEntry{entries[2], dup}, now)
	if arb.HasConflicts() {
		t.Fatalf("identical values must not conflict: %+v", arb.Conflicts)
	}
	// Single entry: no arbitration possible.
	arb = ArbitrateInline(entries[:1], now)
	if arb.HasConflicts() {
		t.Fatal("single entry cannot conflict")
	}
}

func TestLoadForPrompt_RecallArbitrationEndToEnd(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	writeMem(t, dir, "build-old-impl", "build command: go build ./...")
	writeMem(t, dir, "build-new-impl", "build command: go build -tags goolm ./...")
	// Both entries are persistent-category, so both inline. Backdate the
	// older one so the newer (same trust tier) wins the tie-break.
	oldPath := filepath.Join(dir, "build-old-impl.md")
	past := time.Now().Add(-90 * 24 * time.Hour)
	if err := os.Chtimes(oldPath, past, past); err != nil {
		t.Fatal(err)
	}

	inline, _, err := am.LoadForPrompt()
	if err != nil {
		t.Fatal(err)
	}
	if len(inline) != 2 {
		t.Fatalf("expected 2 inline entries, got %d", len(inline))
	}
	loserAnnotated, winnerClean := 0, 0
	for _, e := range inline {
		if strings.Contains(e.Content, "[memory-conflict]") {
			loserAnnotated++
		} else {
			winnerClean++
		}
	}
	if loserAnnotated != 1 || winnerClean != 1 {
		t.Fatalf("want exactly 1 annotated loser and 1 clean winner, got %d/%d", loserAnnotated, winnerClean)
	}
}

func TestRecallArbitration_Cap(t *testing.T) {
	now := time.Now()
	var entries []MemoryEntry
	// One hub entry conflicting with many peers on the same subject.
	entries = append(entries, MemoryEntry{
		Key:     "hub",
		Content: "database engine: postgres",
		Meta:    MemoryMeta{Category: CategoryDefault, CreatedAt: now.Add(-24 * time.Hour)},
	})
	for i := 0; i < maxRecallConflicts+3; i++ {
		entries = append(entries, MemoryEntry{
			Key:     fmt.Sprintf("peer-%d", i), // unique keys so loser dedup cannot collapse them
			Content: "database engine: sqlite",
			Meta:    MemoryMeta{Category: CategoryDefault, CreatedAt: now.Add(-2 * 24 * time.Hour)},
		})
	}
	arb := ArbitrateInline(entries, now)
	if len(arb.Conflicts) != maxRecallConflicts {
		t.Fatalf("conflicts = %d, want capped %d", len(arb.Conflicts), maxRecallConflicts)
	}
}
