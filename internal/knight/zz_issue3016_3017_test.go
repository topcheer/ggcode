package knight

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// TestIssue3016_FailureSignatureSplitsAggregation: unrelated failures that
// share a keyword-derived name must NOT aggregate into one fake-convergence
// candidate; same-signature failures still converge (#3016).
func TestIssue3016_FailureSignatureSplitsAggregation(t *testing.T) {
	mk := func(errMsg string) SkillCandidate {
		return SkillCandidate{
			Name:     "test-failure-recovery",
			Category: "failure-fix",
			Score:    1.0,
			Evidence: []string{errMsg, "fixed"},
		}
	}
	agg := map[string]*candidateAggregate{}
	// Session A: go build timeout; Session B: npm test cannot find module.
	aggregateCandidate(agg, mk("go build timed out after 10m"), "sess-a")
	aggregateCandidate(agg, mk("npm test: cannot find module 'foo'"), "sess-b")
	if got := len(agg); got != 2 {
		t.Fatalf("#3016: keyword-collided failures must aggregate separately, got %d key(s), want 2", got)
	}
	// Review follow-up: the two forks must also carry distinct finalized
	// Names so downstream Scope+Name keyed state (queue/staging/cooldown)
	// cannot cross-wire them.
	finalized := finalizeCandidates(agg)
	names := map[string]bool{}
	for _, c := range finalized {
		if names[c.Name] {
			t.Fatalf("#3016: duplicate finalized Name %q across signature forks", c.Name)
		}
		names[c.Name] = true
	}
	if !names["test-failure-recovery-timeout"] || !names["test-failure-recovery-not-found"] {
		t.Fatalf("#3016: expected signature-suffixed names, got %v", names)
	}
	// Same error class from two sessions still converges (real signal).
	agg2 := map[string]*candidateAggregate{}
	aggregateCandidate(agg2, mk("go test: timed out waiting for build"), "sess-a")
	aggregateCandidate(agg2, mk("npm test timed out after 120s"), "sess-b")
	if got := len(agg2); got != 1 {
		t.Fatalf("#3016: same-class failures must still converge, got %d key(s), want 1", got)
	}
	for _, a := range agg2 {
		if a.candidate.EvidenceCount < 0 || len(a.sessions) != 2 {
			t.Fatalf("#3016: expected 2 source sessions, got %d", len(a.sessions))
		}
	}
	// Non-failure-fix categories keep name-only keys.
	agg3 := map[string]*candidateAggregate{}
	convention := SkillCandidate{Name: "prefer-table-tests", Category: "convention", Score: 1.0, Evidence: []string{"user said prefer table tests"}}
	aggregateCandidate(agg3, convention, "s1")
	aggregateCandidate(agg3, convention, "s2")
	if len(agg3) != 1 {
		t.Fatalf("#3016: convention candidates must aggregate by name alone, got %d", len(agg3))
	}
}

// TestIssue3017_RecordAccountsBeforePersist: a failing disk write must not
// un-account consumed tokens - CanSpend keeps seeing them (#3017).
func TestIssue3017_RecordAccountsBeforePersist(t *testing.T) {
	dir := t.TempDir()
	// A file placed where the knight dir should be forces OpenFile failure.
	rogue := filepath.Join(dir, "knight")
	if err := os.WriteFile(rogue, []byte("not a dir"), 0600); err != nil {
		t.Fatal(err)
	}
	b := NewBudget(dir, config.KnightConfig{DailyTokenBudget: 10000})
	before := b.todayUsed
	if err := b.Record("task", 500, 100); err == nil {
		t.Fatal("#3017: expected write error from rogue path")
	}
	if b.todayUsed != before+600 {
		t.Fatalf("#3017: consumption must stay accounted when write fails: todayUsed=%d, want %d", b.todayUsed, before+600)
	}
}
