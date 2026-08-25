package agent

import (
	"testing"
	"time"
)

// TestIssue1011DeduplicateKeepsLatestLastSeen pins the fix: merging a stale
// duplicate into a kept rule must NOT overwrite the kept rule's fresh
// LastSeen (the old unconditional assignment made the After check dead and
// inverted the semantics to last-wins).
func TestIssue1011DeduplicateKeepsLatestLastSeen(t *testing.T) {
	fresh := time.Now().Add(-20 * time.Minute)
	stale := time.Now().AddDate(0, 0, -21)

	rs := &RuleStore{rules: []Rule{
		{ID: "a", Rule: "run tests with -tags goolm before pushing", MatchPattern: "test", HitCount: 40, LastSeen: fresh},
		{ID: "b", Rule: "run tests with -tags goolm prior to pushing", MatchPattern: "test", HitCount: 1, LastSeen: stale},
	}}
	if n := rs.DeduplicateRules(); n != 1 {
		t.Fatalf("expected the near-duplicate to merge, got %d merges", n)
	}
	if len(rs.rules) != 1 {
		t.Fatalf("expected one kept rule, got %d", len(rs.rules))
	}
	if !rs.rules[0].LastSeen.Equal(fresh) {
		t.Errorf("kept rule's LastSeen must stay fresh (keep-latest), got %v want %v", rs.rules[0].LastSeen, fresh)
	}
	if rs.rules[0].HitCount != 41 {
		t.Errorf("hit counts must sum, got %d want 41", rs.rules[0].HitCount)
	}

	// Opposite direction: stale kept, fresh duplicate -> LastSeen must move
	// forward to the duplicate's time.
	rs2 := &RuleStore{rules: []Rule{
		{ID: "c", Rule: "run tests with -tags goolm before pushing", MatchPattern: "test", HitCount: 1, LastSeen: stale},
		{ID: "d", Rule: "run tests with -tags goolm prior to pushing", MatchPattern: "test", HitCount: 1, LastSeen: fresh},
	}}
	rs2.DeduplicateRules()
	if len(rs2.rules) != 1 {
		t.Fatalf("expected one kept rule, got %d", len(rs2.rules))
	}
	if !rs2.rules[0].LastSeen.Equal(fresh) {
		t.Errorf("merge must adopt the newer LastSeen, got %v want %v", rs2.rules[0].LastSeen, fresh)
	}
}
