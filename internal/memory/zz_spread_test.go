package memory

import (
	"strings"
	"testing"
)

// Spread Activation card (research-loop): one-hop spreading activation in
// RetrieveExcluding - cases with no direct lexical hit but a strong
// associative tie to a direct hit are recalled at a decayed score.

func spreadStore(t *testing.T) (*ExperienceStore, string, string) {
	t.Helper()
	es := NewExperienceStore(spreadTempDir(t))
	// A: direct hit for the query. B: zero lexical overlap with the query,
	// but shares the file path token with A (structural co-edit tie). C: noise.
	aID, _, err := es.Record("fix flaky cron retry", "restart with backoff", "success", []string{"internal/cron/runner.go"})
	if err != nil {
		t.Fatal(err)
	}
	bID, _, err := es.Record("harden scheduler backoff", "jitter the cadence", "success", []string{"internal/cron/runner.go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := es.Record("rewrite docs landing copy", "tone pass", "success", nil); err != nil {
		t.Fatal(err)
	}
	return es, aID, bID
}

func spreadTempDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// Case 1: zero-lexical-overlap case sharing a file with the direct hit is
// recalled, ranked BELOW the direct hit, with a Via audit marker.
func TestSpreadActivationSharedFileRecall(t *testing.T) {
	es, aID, bID := spreadStore(t)
	res, err := es.Retrieve("fix flaky cron retry", 5)
	if err != nil {
		t.Fatal(err)
	}
	var a, b *ScoredExperience
	for i := range res {
		switch res[i].ID {
		case aID:
			a = &res[i]
		case bID:
			b = &res[i]
		}
	}
	if a == nil {
		t.Fatal("direct hit A must be recalled")
	}
	if a.Via != "" {
		t.Fatalf("direct hit must have empty Via, got %q", a.Via)
	}
	if b == nil {
		t.Fatalf("shared-file case B must be recalled via spread: %+v", res)
	}
	if !strings.HasPrefix(b.Via, "spread:") {
		t.Fatalf("spread admit must carry Via marker, got %q", b.Via)
	}
	if b.Score >= a.Score {
		t.Fatalf("spread admit must rank below its linker: b=%v a=%v", b.Score, a.Score)
	}
}

// Case 2: no direct hit -> no spread (no source, no activation).
func TestSpreadRequiresDirectHit(t *testing.T) {
	es, _, _ := spreadStore(t)
	res, err := es.Retrieve("completely unrelated quantum tokens", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 0 {
		t.Fatalf("query with no direct hit must return nothing, got %d", len(res))
	}
}

// Case 3: excluding the linker kills the spread (excludeIDs applies to the
// spread path too).
func TestSpreadExcludesIDs(t *testing.T) {
	es, aID, _ := spreadStore(t)
	res, err := es.RetrieveExcluding("fix flaky cron retry", 5, map[string]bool{aID: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Via != "" {
			t.Fatalf("with the linker excluded, no spread admits may appear: %+v", r)
		}
	}
}
