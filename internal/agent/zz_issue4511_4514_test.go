//go:build goolm

package agent

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// #4514: the autopilot 600-minute default must not leak into the shared
// state - start() used to write it back into s.timeout, so every later
// interactive run on the same Agent inherited the ceiling.
func TestIssue4514_AutopilotDefaultDoesNotLeakAcrossRuns(t *testing.T) {
	s := newSessionTimeoutState(0)

	s.start(true) // autopilot run
	if s.runTimeout != defaultAutopilotSessionTimeout {
		t.Fatalf("autopilot run must resolve the default, got %v", s.runTimeout)
	}
	if s.timeout != 0 {
		t.Fatalf("#4514 regression: configured timeout mutated to %v", s.timeout)
	}

	s.start(false) // later interactive run on the same Agent
	if s.runTimeout != 0 {
		t.Fatalf("#4514 regression: interactive run inherited %v from the prior autopilot run", s.runTimeout)
	}
	if s.timeout != 0 {
		t.Fatalf("configured timeout must stay untouched, got %v", s.timeout)
	}
	if s.check() != "" {
		t.Fatal("unbounded interactive run must not trip the timeout")
	}
}

// #4511: rules with HitCount >= staleRuleMinHits are never crowded out of a
// full store by single-hit newcomers, despite their recency-weighted score
// (3 hits at 100d scores ~0.03 vs 0.5 for a day-old single hit).
func TestIssue4511_HighHitRulesSurviveEviction(t *testing.T) {
	now := time.Now()
	rs := &RuleStore{maxRules: 2, regexCache: map[string]*regexp.Regexp{}}
	rs.rules = []Rule{
		{Rule: "veteran rule", HitCount: 5, LastSeen: now.Add(-100 * 24 * time.Hour)}, // protected, stale
		{Rule: "newcomer A", HitCount: 1, LastSeen: now.Add(-1 * 24 * time.Hour)},     // score 0.5
		{Rule: "newcomer B", HitCount: 1, LastSeen: now.Add(-1 * 24 * time.Hour)},     // score 0.5
	}
	rs.evict()
	if len(rs.rules) != 2 {
		t.Fatalf("expected exactly maxRules survivors, got %d: %v", len(rs.rules), rs.rules)
	}
	var vet bool
	for _, r := range rs.rules {
		if strings.Contains(r.Rule, "veteran") {
			vet = true
		}
	}
	if !vet {
		t.Fatalf("#4511 regression: 5-hit veteran evicted in favor of single-hit newcomers: %v", rs.rules)
	}
}
