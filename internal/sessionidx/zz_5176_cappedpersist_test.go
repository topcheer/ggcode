package sessionidx

import (
	"os"
	"path/filepath"
	"testing"
)

// Pin for the #5176 follow-up: a capped index must NOT persist its emptied
// postings. Old behavior: Refresh() saved {Trigrams:{}, IndexedTo:EOF}; on
// reload Load() computed approxBytes=Size()~0, SetSelfBudget never re-capped,
// and QueryScoped's source-scan fallback (Capped()-gated) never engaged ->
// all pre-cap content silently unfindable after restart.
func TestSelfCap_CappedIndexIsNotPersisted(t *testing.T) {
	dir := t.TempDir()
	const id = "selfcap2"
	seedSelfCapSession(t, dir, id, 40)

	// Phase 1: uncapped ingest -> persists good postings to disk.
	ix, err := Load(dir, id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := ix.Refresh(); err != nil {
		t.Fatalf("refresh1: %v", err)
	}
	if ix.Capped() {
		t.Fatal("phase 1 must be uncapped (no budget set)")
	}
	if _, err := os.Stat(filepath.Join(IdxDir(dir), id+".idx")); err != nil {
		t.Fatalf("uncapped refresh must persist the idx: %v", err)
	}

	// Phase 2: shrink the budget so the next Refresh trips the cap.
	ix.SetSelfBudget(64) // retroactive drop: already over 64 bytes
	if err := ix.Refresh(); err != nil {
		t.Fatalf("refresh2: %v", err)
	}
	if !ix.Capped() {
		t.Fatal("expected capped=true after budget shrink")
	}

	// Reload cycle: last-good idx must survive on disk (not the emptied one),
	// and SetSelfBudget must re-cap it so the degraded scan re-engages.
	ix2, err := Load(dir, id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if ix2.Size() == 0 {
		t.Fatal("disk idx was overwritten with empty postings - capped Refresh must not persist")
	}
	ix2.SetSelfBudget(64)
	if !ix2.Capped() {
		t.Fatal("reload of an over-budget idx must re-cap via the retroactive drop")
	}
}
