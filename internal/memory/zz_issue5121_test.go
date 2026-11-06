package memory

import "testing"

// Issue #5121: an exact same-key re-save is an INTENTIONAL overwrite under
// the r488 archive-on-overwrite contract, not a duplicate. CheckDuplicate
// must expose the same-key hit via SameKey=true so callers can suppress the
// duplicate warning; different-key/similar-content collisions must keep
// SameKey=false so the warning still fires.
func TestIssue5121SameKeyOverwriteIsNotDuplicateWarning(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}

	if err := am.SaveMemory("pool-snapshot", "round 1 content"); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	// Overwrite the same key with CHANGED content N times: every check must
	// carry SameKey=true (caller suppresses the warning).
	for round := 2; round <= 4; round++ {
		dc := am.CheckDuplicate("pool-snapshot", "round "+string(rune('0'+round))+" content")
		if !dc.IsDuplicate() {
			t.Fatalf("round %d: expected IsDuplicate (same-key semantics retained)", round)
		}
		if !dc.SameKey {
			t.Errorf("round %d: same-key hit must set SameKey=true, got false (SimilarTo=%q)", round, dc.SimilarTo)
		}
		if dc.SimilarTo != "pool-snapshot" {
			t.Errorf("round %d: SimilarTo must be the same key, got %q", round, dc.SimilarTo)
		}
	}
}

func TestIssue5121NearKeyStillFiresDuplicate(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}

	if err := am.SaveMemory("build-process", "content 1"); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	dc := am.CheckDuplicate("build-process-v2", "content 1")
	if !dc.IsDuplicate() {
		t.Fatal("near-duplicate different key must still classify as duplicate")
	}
	if dc.SameKey {
		t.Errorf("different-key collision must keep SameKey=false, got true (SimilarTo=%q)", dc.SimilarTo)
	}
}
