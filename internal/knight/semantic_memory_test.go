package knight

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func newSemanticUtilityTestStore(t *testing.T) *semanticMemoryStore {
	t.Helper()
	return newSemanticMemoryStore(filepath.Join(t.TempDir(), "knight-memory.jsonl"))
}

func mustAppend(t *testing.T, s *semanticMemoryStore, summary string) SemanticMemoryEntry {
	t.Helper()
	if err := s.Append(SemanticMemoryEntry{Kind: "lesson", Summary: summary}); err != nil {
		t.Fatalf("Append(%q): %v", summary, err)
	}
	entries, err := s.Recent(1)
	if err != nil || len(entries) == 0 {
		t.Fatalf("Recent after Append(%q): %v (entries=%d)", summary, err, len(entries))
	}
	return entries[0]
}

func utilityOf(t *testing.T, entries []SemanticMemoryEntry, id string) float64 {
	t.Helper()
	for _, e := range entries {
		if e.ID == id {
			return entryUtility(e)
		}
	}
	t.Fatalf("entry %q not found among %d entries", id, len(entries))
	return 0
}

func storeUtility(t *testing.T, s *semanticMemoryStore, id string) float64 {
	t.Helper()
	entries, err := s.Recent(maxSemanticMemoryEntries)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	return utilityOf(t, entries, id)
}

// #r25: recurring lessons merge (semantic dedup) and gain utility instead of
// flooding the store with near-identical rows.
func TestSemanticMemoryDedupMergesRecurrence(t *testing.T) {
	s := newSemanticUtilityTestStore(t)

	first := mustAppend(t, s, "Prefer table-driven tests for parser edge cases")
	other := mustAppend(t, s, "Run gofmt before committing staged changes")

	// exact recurrence merges into the existing entry
	if err := s.Append(SemanticMemoryEntry{Kind: "lesson", Summary: "Prefer table-driven tests for parser edge cases"}); err != nil {
		t.Fatalf("duplicate Append: %v", err)
	}

	entries, err := s.Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("store has %d entries after recurrence, want 2 (dedup merge)", len(entries))
	}
	var merged SemanticMemoryEntry
	for _, e := range entries {
		if e.ID == first.ID {
			merged = e
		}
	}
	if merged.Hits != 2 {
		t.Fatalf("merged entry Hits = %d, want 2", merged.Hits)
	}
	if entryUtility(merged) <= entryUtility(other) && merged.Time.Equal(other.Time) {
		// utility should have risen above the 0.5 baseline after the merge
		t.Fatalf("merged utility %v did not rise above baseline", entryUtility(merged))
	}
	if entryUtility(merged) <= 0.5 {
		t.Fatalf("merged utility %v, want > 0.5 after recurrence", entryUtility(merged))
	}
}

// #r25: retrieval ranks by empirical utility, not raw recency.
func TestTopByUtilityRanksByScore(t *testing.T) {
	s := newSemanticUtilityTestStore(t)

	low := mustAppend(t, s, "Always check error returns before using results")
	high := mustAppend(t, s, "Guard concurrent map access with a mutex")

	if err := s.Reinforce(high.ID, 0.4); err != nil {
		t.Fatalf("Reinforce(high): %v", err)
	}
	if err := s.Reinforce(low.ID, -0.4); err != nil {
		t.Fatalf("Reinforce(low): %v", err)
	}

	top, err := s.TopByUtility(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 2 {
		t.Fatalf("TopByUtility = %d entries, want 2", len(top))
	}
	if top[0].ID != high.ID {
		t.Fatalf("TopByUtility[0] = %q (%.2f), want reinforced %q", top[0].ID, entryUtility(top[0]), high.ID)
	}
	if top[1].ID != low.ID {
		t.Fatalf("TopByUtility[1] = %q, want penalized %q", top[1].ID, low.ID)
	}
}

func TestReinforceMissingIDIsNoop(t *testing.T) {
	s := newSemanticUtilityTestStore(t)
	before := mustAppend(t, s, "Single stable lesson")

	if err := s.Reinforce("does-not-exist", 0.5); err != nil {
		t.Fatalf("Reinforce(missing) err = %v, want nil noop", err)
	}
	entries, err := s.Recent(5)
	if err != nil || len(entries) != 1 {
		t.Fatalf("store changed after noop reinforce: %v (n=%d)", err, len(entries))
	}
	if entryUtility(entries[0]) != entryUtility(before) {
		t.Fatalf("utility changed after noop reinforce")
	}
}

func TestReinforceClampsToUnitRange(t *testing.T) {
	s := newSemanticUtilityTestStore(t)
	e := mustAppend(t, s, "Clamped lesson about utility saturation")

	for i := 0; i < 30; i++ {
		if err := s.Reinforce(e.ID, 0.2); err != nil {
			t.Fatalf("Reinforce #%d: %v", i, err)
		}
	}
	if u := storeUtility(t, s, e.ID); u > 1.0 {
		t.Fatalf("utility %v exceeds clamp 1.0", u)
	}

	for i := 0; i < 30; i++ {
		if err := s.Reinforce(e.ID, -0.2); err != nil {
			t.Fatalf("Reinforce #%d: %v", i, err)
		}
	}
	if u := storeUtility(t, s, e.ID); u < 0.0 {
		t.Fatalf("utility %v below clamp 0.0", u)
	}
}

// #r25: eviction prunes lowest utility first, so a strongly reinforced old
// lesson survives while unreinforced chaff is dropped.
func TestEvictionKeepsHighUtilityEntry(t *testing.T) {
	s := newSemanticUtilityTestStore(t)

	keeper := mustAppend(t, s, "High value lesson zero keep me forever")
	if err := s.Reinforce(keeper.ID, 0.5); err != nil {
		t.Fatalf("Reinforce(keeper): %v", err)
	}
	for i := 1; i <= maxSemanticMemoryEntries+5; i++ {
		if err := s.Append(SemanticMemoryEntry{
			Kind:    "lesson",
			Summary: fmt.Sprintf("chaff lesson number %d with unique marker token %d padding", i, i*7919),
			Time:    time.Now().UTC().Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatalf("Append chaff %d: %v", i, err)
		}
	}

	entries, err := s.Recent(maxSemanticMemoryEntries * 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > maxSemanticMemoryEntries {
		t.Fatalf("store grew to %d entries, want cap %d", len(entries), maxSemanticMemoryEntries)
	}
	found := false
	for _, e := range entries {
		if e.ID == keeper.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("utility-aware eviction dropped the highest-utility entry")
	}
}

// #r25: the eval injection path returns both the rendered text and the
// injected entry IDs so the scheduler can feed decisions back.
func TestFormatSemanticMemoryForEvalIDsAndFeedback(t *testing.T) {
	k := &Knight{projDir: t.TempDir()}

	// empty store → zero context (caller substitutes placeholder)
	if ctx := k.formatSemanticMemoryForEvalIDs(4); len(ctx.ids) != 0 || ctx.text != "" {
		t.Fatalf("empty store yielded ids=%d text=%q, want zero/empty", len(ctx.ids), ctx.text)
	}

	strong := mustAppend(t, newSemanticMemoryStore(k.semanticMemoryPath()),
		"Keep diffs minimal and reviewable")
	weak := mustAppend(t, newSemanticMemoryStore(k.semanticMemoryPath()),
		"Prefer configuration over hardcoded constants")
	if err := k.ReinforceSemanticMemory(strong.ID, 0.4); err != nil {
		t.Fatalf("ReinforceSemanticMemory: %v", err)
	}

	ctx := k.formatSemanticMemoryForEvalIDs(1)
	if len(ctx.ids) != 1 || ctx.ids[0] != strong.ID {
		t.Fatalf("ids = %v, want [%s] (top-utility entry)", ctx.ids, strong.ID)
	}
	if ctx.text == "" {
		t.Fatal("injection text empty despite populated store")
	}

	// scheduler feedback contract: after the eval declines, the injected
	// lesson's utility must decrease via the same Reinforce call the
	// scheduler makes.
	before := storeUtility(t, newSemanticMemoryStore(k.semanticMemoryPath()), strong.ID)
	if err := k.ReinforceSemanticMemory(ctx.ids[0], -0.05); err != nil {
		t.Fatalf("feedback Reinforce: %v", err)
	}
	after := storeUtility(t, newSemanticMemoryStore(k.semanticMemoryPath()), strong.ID)
	if after >= before {
		t.Fatalf("decline feedback did not lower utility: %v -> %v", before, after)
	}
	_ = weak // distinct second lesson so dedup cannot merge the two
}

// legacy JSONL records without utility fields map to the 0.5 default and
// rank fairly against fresh entries.
func TestLegacyEntriesDefaultUtility(t *testing.T) {
	s := newSemanticUtilityTestStore(t)
	fresh := mustAppend(t, s, "Fresh lesson written by new code")
	if err := s.Reinforce(fresh.ID, -0.3); err != nil {
		t.Fatal(err)
	}

	legacy := SemanticMemoryEntry{
		ID:      "mem-legacy-1",
		Time:    time.Now().UTC().Add(-time.Hour),
		Kind:    "lesson",
		Summary: "Legacy lesson persisted before utility scoring existed",
	}
	if err := s.Append(legacy); err != nil {
		t.Fatalf("Append legacy: %v", err)
	}

	entries, err := s.Recent(5)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.ID == legacy.ID && entryUtility(e) != 0.5 {
			t.Fatalf("legacy entry utility = %v, want 0.5 default", entryUtility(e))
		}
	}

	top, err := s.TopByUtility(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) == 0 || top[0].ID != legacy.ID {
		t.Fatalf("legacy entry (0.5) should outrank penalized fresh (%.2f)", entryUtility(fresh)-0.3)
	}
}
