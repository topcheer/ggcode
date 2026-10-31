package agent

import (
	"strings"
	"testing"
	"time"
)

// #4595: two rules sharing a broad ToolPattern but with unrelated semantics
// (low Jaccard) must NOT merge - the +0.3 bonus used to lower the effective
// threshold to Jaccard 0.45, silently swallowing the newcomer.
func TestIssue4595_UnrelatedSameToolRulesDoNotMerge(t *testing.T) {
	a := Rule{
		ID:          "r-a",
		Rule:        "always run gofmt before committing go files in this repo",
		ToolPattern: `git_commit\..*`,
		HitCount:    2,
		LastSeen:    time.Now(),
	}
	b := Rule{
		ID:          "r-b",
		Rule:        "include ticket number from branch name in the commit message body",
		ToolPattern: `git_commit\..*`,
		HitCount:    1,
		LastSeen:    time.Now(),
	}

	sim := ruleSimilarity(a, b)
	if sim >= 0.75 {
		t.Fatalf("unrelated same-tool rules scored %.2f >= 0.75; bonus must not close the gap for low-Jaccard pairs", sim)
	}

	rs := NewRuleStore(t.TempDir())
	rs.AddRule(a)
	n := len(rs.Rules())
	rs.AddRule(b)
	if got := len(rs.Rules()); got != n+1 {
		t.Fatalf("newcomer was merged away: %d rules after add, want %d", got, n+1)
	}
}

// The original bonus intent still holds: same concept, low Jaccard, same
// tool - these SHOULD merge (Jaccard here >= 0.3 gate).
func TestIssue4595_RelatedSameToolRulesStillMerge(t *testing.T) {
	a := Rule{
		ID:          "r-a",
		Rule:        "run gofmt on edited go files before committing",
		ToolPattern: `git_commit\..*`,
		HitCount:    2,
		LastSeen:    time.Now(),
	}
	b := Rule{
		ID:          "r-b",
		Rule:        "always gofmt edited files before committing staged work",
		ToolPattern: `git_commit\..*`,
		HitCount:    1,
		LastSeen:    time.Now(),
	}

	if sim := ruleSimilarity(a, b); sim < 0.75 {
		t.Fatalf("same-concept pair scored %.2f < 0.75; gate regressed the original merge intent", sim)
	}
}

// #4595: when a merge does happen, the existing rule must inherit the
// newcomer's MatchPattern/FixHint if it never had them.
func TestIssue4595_MergeKeepsMatchPatternAndFixHint(t *testing.T) {
	rs := NewRuleStore(t.TempDir())
	first := Rule{
		ID:          "r-first",
		Rule:        "run gofmt on edited go files before committing",
		ToolPattern: `git_commit\..*`,
		HitCount:    2,
		LastSeen:    time.Now(),
	}
	dup := Rule{
		ID:           "r-dup",
		Rule:         "always gofmt edited files before committing staged work",
		ToolPattern:  `git_commit\..*`,
		MatchPattern: `^diff.*\+\+\+ .*\.go$`,
		FixHint:      "gofmt -w the touched files",
		HitCount:     1,
		LastSeen:     time.Now(),
	}
	rs.AddRule(first)
	rs.AddRule(dup)

	rules := rs.Rules()
	if len(rules) != 1 {
		t.Fatalf("expected merged single rule, got %d", len(rules))
	}
	if rules[0].MatchPattern == "" || rules[0].FixHint == "" {
		t.Fatalf("#4595 merge dropped fields: MatchPattern=%q FixHint=%q", rules[0].MatchPattern, rules[0].FixHint)
	}
	if !strings.Contains(rules[0].FixHint, "gofmt") {
		t.Fatalf("unexpected FixHint %q", rules[0].FixHint)
	}
	if rules[0].HitCount != 3 {
		t.Fatalf("HitCount=%d, want 3 (2+1)", rules[0].HitCount)
	}
}
